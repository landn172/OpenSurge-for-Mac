package runtime

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Component identifies an executable that was accepted for a gateway runtime.
// It is persisted with the runtime state so a later component activation can
// prove exactly which binary it is replacing, rather than trusting a mutable
// PATH lookup or a symlink that may have changed after startup.
type Component struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

func InspectComponent(name, binary string, versionArgs ...string) (Component, error) {
	resolved, err := resolveExecutable(binary)
	if err != nil {
		return Component{}, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return Component{}, err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return Component{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, resolved, versionArgs...).CombinedOutput()
	if err != nil {
		return Component{}, fmt.Errorf("read %s version: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	version := strings.TrimSpace(string(output))
	if first, _, ok := strings.Cut(version, "\n"); ok {
		version = strings.TrimSpace(first)
	}
	if version == "" {
		return Component{}, fmt.Errorf("read %s version: command produced no output", name)
	}
	return Component{Name: name, Path: resolved, Version: version, SHA256: fmt.Sprintf("%x", digest.Sum(nil))}, nil
}

func InspectGatewayComponents(mihomoBinary, dnsmasqBinary string, includeDNSMasq bool) ([]Component, error) {
	mihomo, err := InspectComponent("mihomo", mihomoBinary, "-v")
	if err != nil {
		return nil, err
	}
	components := []Component{mihomo}
	if !includeDNSMasq {
		return components, nil
	}
	dnsmasq, err := InspectComponent("dnsmasq", dnsmasqBinary, "--version")
	if err != nil {
		return nil, err
	}
	return append(components, dnsmasq), nil
}

func ReplaceComponent(components []Component, replacement Component) []Component {
	result := append([]Component(nil), components...)
	for index, component := range result {
		if component.Name == replacement.Name {
			result[index] = replacement
			return result
		}
	}
	return append(result, replacement)
}

func resolveExecutable(binary string) (string, error) {
	if strings.ContainsRune(binary, os.PathSeparator) {
		resolved, err := filepath.EvalSymlinks(binary)
		if err != nil {
			return "", err
		}
		return filepath.Abs(resolved)
	}
	return exec.LookPath(binary)
}
