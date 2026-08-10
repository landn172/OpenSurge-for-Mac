package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestShellAndNotices(t *testing.T) {
	locked, err := loadManifest(filepath.Join("..", "..", "dependencies", "runtime.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var shell strings.Builder
	file, err := os.CreateTemp(t.TempDir(), "shell")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeShell(file, locked, "arm64"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	shell.Write(data)
	if !strings.Contains(shell.String(), "MIHOMO_VERSION=\"1.19.27\"") || !strings.Contains(shell.String(), "DNSMASQ_VERSION=\"2.93\"") {
		t.Fatalf("unexpected shell output: %s", shell.String())
	}
	notices := noticeBlock(locked)
	if !strings.Contains(notices, "runtime-dependencies:start") || !strings.Contains(notices, "mihomo-darwin-amd64-compatible-v1.19.27.gz") {
		t.Fatalf("unexpected notices: %s", notices)
	}
}

func TestManifestRejectsDuplicateArtifact(t *testing.T) {
	locked, err := loadManifest(filepath.Join("..", "..", "dependencies", "runtime.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	locked.Components[1].Artifacts = append(locked.Components[1].Artifacts, locked.Components[1].Artifacts[0])
	if err := validateManifest(locked); err == nil {
		t.Fatal("duplicate artifact was accepted")
	}
}
