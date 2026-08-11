package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
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

	runner := controlapi.ActionRunner(controlapi.HelperClient{SocketPath: *helperSocket})
	if *direct {
		runner = controlapi.DirectRunner{}
	}
	server, err := controlapi.New(controlapi.Options{
		ConfigPath:   *configPath,
		Addr:         *addr,
		StoreDir:     *storeDir,
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
		// Printed as a plain URL for development. In the product this value is
		// rendered as a QR code by the menubar app; the 30-second code lifetime
		// makes retyping it impractical.
		if mobileURL, err := server.MobileBootstrapURL("dashboard"); err == nil {
			fmt.Printf("Open on a phone (read-only): %s\n", mobileURL)
		}
	}
	if err := server.Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
