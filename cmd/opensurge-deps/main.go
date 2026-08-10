// opensurge-deps keeps the release-critical runtime dependency metadata in one
// checked-in lock file. It deliberately does not download or activate software:
// downloading remains in the release script and activation remains an installer
// transaction, both of which have their own privilege and rollback boundaries.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/dhcp"
	"open-mihomo-gateway/internal/mihomo"
	"open-mihomo-gateway/internal/runtime"
)

const defaultManifest = "dependencies/runtime.lock.json"

var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type manifest struct {
	SchemaVersion int         `json:"schema_version"`
	Components    []component `json:"components"`
}

type component struct {
	Name         string     `json:"name"`
	Version      string     `json:"version"`
	Distribution string     `json:"distribution"`
	License      string     `json:"license"`
	UpstreamURL  string     `json:"upstream_url,omitempty"`
	SourceURL    string     `json:"source_url"`
	Archive      string     `json:"archive,omitempty"`
	SHA256       string     `json:"sha256,omitempty"`
	Artifacts    []artifact `json:"artifacts,omitempty"`
}

type artifact struct {
	Arch    string `json:"arch"`
	Archive string `json:"archive"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr *os.File) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	command := args[0]
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	manifestPath := fs.String("manifest", defaultManifest, "runtime dependency lock file")
	arch := fs.String("arch", "", "macOS architecture (arm64 or x86_64)")
	noticesPath := fs.String("notices", "THIRD_PARTY_NOTICES.md", "third-party notices file")
	releaseNotesPath := fs.String("release-notes", "packaging/unsigned-release-notes.md", "unsigned release notes file")
	configPath := fs.String("config", "examples/config.example.yaml", "gateway config to validate")
	mihomoPath := fs.String("mihomo", "", "prepared mihomo binary")
	dnsmasqPath := fs.String("dnsmasq", "", "prepared dnsmasq binary")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	locked, err := loadManifest(*manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "runtime dependency lock: %v\n", err)
		return 1
	}
	switch command {
	case "shell":
		if *arch == "" {
			fmt.Fprintln(stderr, "shell: --arch is required")
			return 2
		}
		if err := writeShell(stdout, locked, *arch); err != nil {
			fmt.Fprintf(stderr, "shell: %v\n", err)
			return 1
		}
	case "notices":
		fmt.Fprint(stdout, noticeBlock(locked))
	case "release-notes":
		fmt.Fprint(stdout, releaseNotesBlock(locked))
	case "check":
		if err := checkNotices(*noticesPath, noticeBlock(locked)); err != nil {
			fmt.Fprintf(stderr, "runtime dependency check: %v\n", err)
			return 1
		}
		if err := checkReleaseNotes(*releaseNotesPath, releaseNotesBlock(locked)); err != nil {
			fmt.Fprintf(stderr, "runtime dependency check: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "runtime dependency lock and notices are synchronized")
	case "validate":
		if *mihomoPath == "" || *dnsmasqPath == "" {
			fmt.Fprintln(stderr, "validate: --mihomo and --dnsmasq are required")
			return 2
		}
		if err := validatePreparedBinaries(*configPath, *mihomoPath, *dnsmasqPath); err != nil {
			fmt.Fprintf(stderr, "runtime dependency validation: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "prepared mihomo and dnsmasq binaries accepted the generated gateway configuration")
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", command)
		usage(stderr)
		return 2
	}
	return 0
}

func usage(w *os.File) {
	fmt.Fprintln(w, "usage: opensurge-deps <shell|notices|release-notes|check|validate> [flags]")
}

func loadManifest(path string) (manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, err
	}
	var locked manifest
	if err := json.Unmarshal(data, &locked); err != nil {
		return manifest{}, err
	}
	if err := validateManifest(locked); err != nil {
		return manifest{}, err
	}
	return locked, nil
}

func validateManifest(locked manifest) error {
	if locked.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1")
	}
	if len(locked.Components) == 0 {
		return fmt.Errorf("components must not be empty")
	}
	seen := map[string]bool{}
	for _, component := range locked.Components {
		if component.Name == "" || component.Version == "" || component.License == "" || component.SourceURL == "" {
			return fmt.Errorf("component requires name, version, license, and source_url")
		}
		if seen[component.Name] {
			return fmt.Errorf("duplicate component %q", component.Name)
		}
		seen[component.Name] = true
		if !strings.HasPrefix(component.SourceURL, "https://") {
			return fmt.Errorf("%s source_url must use https", component.Name)
		}
		switch component.Distribution {
		case "source":
			if component.Archive == "" || !sha256Pattern.MatchString(component.SHA256) {
				return fmt.Errorf("%s source distribution requires archive and lowercase SHA-256", component.Name)
			}
		case "upstream_binary":
			if !strings.HasPrefix(component.UpstreamURL, "https://") {
				return fmt.Errorf("%s upstream_url must use https", component.Name)
			}
			if len(component.Artifacts) == 0 {
				return fmt.Errorf("%s upstream binary distribution requires artifacts", component.Name)
			}
			arches := map[string]bool{}
			for _, artifact := range component.Artifacts {
				if artifact.Arch == "" || artifact.Archive == "" || !strings.HasPrefix(artifact.URL, "https://") || !sha256Pattern.MatchString(artifact.SHA256) {
					return fmt.Errorf("%s has an incomplete artifact", component.Name)
				}
				if arches[artifact.Arch] {
					return fmt.Errorf("%s has duplicate %s artifact", component.Name, artifact.Arch)
				}
				arches[artifact.Arch] = true
			}
		default:
			return fmt.Errorf("%s has unsupported distribution %q", component.Name, component.Distribution)
		}
	}
	for _, name := range []string{"dnsmasq", "mihomo"} {
		if _, err := findComponent(locked, name); err != nil {
			return err
		}
	}
	return nil
}

func findComponent(locked manifest, name string) (component, error) {
	for _, component := range locked.Components {
		if component.Name == name {
			return component, nil
		}
	}
	return component{}, fmt.Errorf("required component %q is missing", name)
}

func findArtifact(component component, arch string) (artifact, error) {
	for _, artifact := range component.Artifacts {
		if artifact.Arch == arch {
			return artifact, nil
		}
	}
	return artifact{}, fmt.Errorf("%s does not provide a %s artifact", component.Name, arch)
}

func writeShell(w *os.File, locked manifest, arch string) error {
	dnsmasq, err := findComponent(locked, "dnsmasq")
	if err != nil {
		return err
	}
	mihomo, err := findComponent(locked, "mihomo")
	if err != nil {
		return err
	}
	artifact, err := findArtifact(mihomo, arch)
	if err != nil {
		return err
	}
	values := []struct{ key, value string }{
		{"DNSMASQ_VERSION", dnsmasq.Version}, {"DNSMASQ_SHA256", dnsmasq.SHA256}, {"DNSMASQ_ARCHIVE", dnsmasq.Archive}, {"DNSMASQ_URL", dnsmasq.SourceURL},
		{"MIHOMO_VERSION", mihomo.Version}, {"MIHOMO_SHA256", artifact.SHA256}, {"MIHOMO_ARCHIVE", artifact.Archive}, {"MIHOMO_URL", artifact.URL},
	}
	for _, value := range values {
		fmt.Fprintf(w, "%s=%q\n", value.key, value.value)
	}
	return nil
}

func noticeBlock(locked manifest) string {
	dnsmasq, _ := findComponent(locked, "dnsmasq")
	mihomo, _ := findComponent(locked, "mihomo")
	artifacts := append([]artifact(nil), mihomo.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Arch < artifacts[j].Arch })
	var out strings.Builder
	out.WriteString("<!-- runtime-dependencies:start -->\n")
	out.WriteString("## mihomo\n\n")
	fmt.Fprintf(&out, "- Version: `%s`\n- License: `%s`\n", mihomo.Version, mihomo.License)
	out.WriteString("- Distributed forms (each architecture-specific installer contains the matching binary):\n")
	for _, artifact := range artifacts {
		label := map[string]string{"arm64": "Apple Silicon", "x86_64": "Intel"}[artifact.Arch]
		if label == "" {
			label = artifact.Arch
		}
		fmt.Fprintf(&out, "  - %s: unmodified upstream `%s`\n    - Binary SHA-256:\n      `%s`\n", label, artifact.Archive, artifact.SHA256)
	}
	fmt.Fprintf(&out, "- Upstream: <%s>\n- Corresponding source:\n  <%s>\n- License text: [`LICENSE`](LICENSE)\n\n", mihomo.UpstreamURL, mihomo.SourceURL)
	out.WriteString("## dnsmasq\n\n")
	fmt.Fprintf(&out, "- Version: `%s`\n- License: `%s`, at the recipient's option\n", dnsmasq.Version, dnsmasq.License)
	out.WriteString("- Distributed form: built from unmodified upstream source for Apple Silicon or\n  Intel macOS by [`scripts/prepare-gui-release-deps.sh`](scripts/prepare-gui-release-deps.sh)\n")
	fmt.Fprintf(&out, "- Source archive SHA-256:\n  `%s`\n- Corresponding source:\n  <%s>\n", dnsmasq.SHA256, dnsmasq.SourceURL)
	out.WriteString("- License texts: [`third_party/licenses/dnsmasq-COPYING`](third_party/licenses/dnsmasq-COPYING)\n  and [`LICENSE`](LICENSE)\n")
	out.WriteString("<!-- runtime-dependencies:end -->\n")
	return out.String()
}

func checkNotices(path, expected string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	start := strings.Index(text, "<!-- runtime-dependencies:start -->")
	end := strings.Index(text, "<!-- runtime-dependencies:end -->")
	if start < 0 || end < start {
		return errors.New("runtime dependency notice markers are missing or malformed")
	}
	end += len("<!-- runtime-dependencies:end -->")
	if strings.TrimSpace(text[start:end]) != strings.TrimSpace(expected) {
		return errors.New("runtime dependency notices differ from dependencies/runtime.lock.json; run `go run ./cmd/opensurge-deps notices` and update the marked block")
	}
	return nil
}

func releaseNotesBlock(locked manifest) string {
	dnsmasq, _ := findComponent(locked, "dnsmasq")
	mihomo, _ := findComponent(locked, "mihomo")
	var out strings.Builder
	out.WriteString("<!-- runtime-dependencies:zh:start -->\n")
	fmt.Fprintf(&out, "- mihomo %s 源码：<%s>\n- dnsmasq %s 源码：<%s>\n", mihomo.Version, mihomo.SourceURL, dnsmasq.Version, dnsmasq.SourceURL)
	out.WriteString("<!-- runtime-dependencies:zh:end -->\n")
	out.WriteString("<!-- runtime-dependencies:en:start -->\n")
	fmt.Fprintf(&out, "- mihomo %s source: <%s>\n- dnsmasq %s source: <%s>\n", mihomo.Version, mihomo.SourceURL, dnsmasq.Version, dnsmasq.SourceURL)
	out.WriteString("<!-- runtime-dependencies:en:end -->\n")
	return out.String()
}

func checkReleaseNotes(path, expected string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	for _, markers := range [][2]string{
		{"<!-- runtime-dependencies:zh:start -->", "<!-- runtime-dependencies:zh:end -->"},
		{"<!-- runtime-dependencies:en:start -->", "<!-- runtime-dependencies:en:end -->"},
	} {
		start := strings.Index(text, markers[0])
		end := strings.Index(text, markers[1])
		if start < 0 || end < start {
			return errors.New("runtime dependency release-note markers are missing or malformed")
		}
		end += len(markers[1])
		expectedStart := strings.Index(expected, markers[0])
		expectedEnd := strings.Index(expected, markers[1]) + len(markers[1])
		if strings.TrimSpace(text[start:end]) != strings.TrimSpace(expected[expectedStart:expectedEnd]) {
			return errors.New("runtime dependency release notes differ from dependencies/runtime.lock.json; run `go run ./cmd/opensurge-deps release-notes` and update the marked blocks")
		}
	}
	return nil
}

func validatePreparedBinaries(configPath, mihomoPath, dnsmasqPath string) error {
	for _, path := range []string{mihomoPath, dnsmasqPath} {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("%s is not an executable regular file", path)
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "opensurge-runtime-dependency-validation-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	cfg.Mihomo.Binary = mihomoPath
	cfg.DHCP.Binary = dnsmasqPath
	cfg.Runtime.Dir = temp
	cfg.Mihomo.Config = filepath.Join(temp, "mihomo.yaml")
	if err := config.PrepareDevicePolicy(&cfg); err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		return err
	}
	if err := mihomo.New(cfg, paths).ValidateConfig(); err != nil {
		return err
	}
	if err := dhcp.New(cfg, paths).WriteConfig(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	command := exec.CommandContext(ctx, dnsmasqPath, "--test", "--conf-file="+paths.DNSMasqConf)
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("dnsmasq generated-config validation failed: %w: %s", err, strings.TrimSpace(output.String()))
	}
	return nil
}
