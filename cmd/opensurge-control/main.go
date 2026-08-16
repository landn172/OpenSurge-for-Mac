package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"open-mihomo-gateway/internal/controlapi"
	"open-mihomo-gateway/internal/webui"
)

func main() {
	configPath := flag.String("config", "examples/config.example.yaml", "path to gateway config")
	addr := flag.String("addr", "127.0.0.1:61767", "loopback listen address")
	storeDir := flag.String("store", "", "application support directory")
	helperSocket := flag.String("helper-socket", "/var/run/opensurge/helper.sock", "privileged helper socket")
	direct := flag.Bool("direct-root", false, "run actions directly; requires root and is intended for development")
	lanInterface := flag.String("mobile-interface", "", "interface name whose IPv4 address also accepts connections, enabling the read-only phone surface; empty keeps the service loopback-only")
	flag.Parse()
	resolvedStoreDir := *storeDir
	if resolvedStoreDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		resolvedStoreDir = filepath.Join(home, "Library", "Application Support", "OpenSurge")
	}
	// The packaged LaunchAgent intentionally omits -mobile-interface. If a
	// user has made a GUI choice, it is authoritative even when an older local
	// LaunchAgent still carries that legacy argument, so turning the switch off
	// cannot be undone by a later service restart.
	store := controlapi.NewStore(resolvedStoreDir)
	if configured, err := store.HasMobileAccessSettings(); err != nil {
		fmt.Fprintln(os.Stderr, "check mobile access settings:", err)
		os.Exit(1)
	} else if configured {
		settings, err := store.MobileAccess()
		if err != nil {
			fmt.Fprintln(os.Stderr, "read mobile access settings:", err)
			os.Exit(1)
		}
		*lanInterface = ""
		if settings.Enabled {
			*lanInterface = settings.Interface
		}
	}

	runner := controlapi.ActionRunner(controlapi.HelperClient{SocketPath: *helperSocket})
	if *direct {
		runner = controlapi.DirectRunner{}
	}
	server, err := controlapi.New(controlapi.Options{
		ConfigPath:   *configPath,
		Addr:         *addr,
		StoreDir:     resolvedStoreDir,
		Runner:       runner,
		Static:       webui.Handler(),
		LANInterface: *lanInterface,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Printf("OpenSurge Control API: %s\n", *addr)
	fmt.Printf("Open Web GUI: %s\n", server.BootstrapURL())
	if server.MobileAccessEnabled() {
		fmt.Println("Mobile access is on. Pair a phone from the Web GUI's paired-devices page.")
	}
	if err := server.Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
