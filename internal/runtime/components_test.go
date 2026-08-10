package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectComponentRecordsResolvedPathVersionAndDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "component")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho component 1.2.3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	component, err := InspectComponent("fixture", path, "--version")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if component.Name != "fixture" || component.Path != resolved || component.Version != "component 1.2.3" || len(component.SHA256) != 64 {
		t.Fatalf("component = %#v", component)
	}
}

func TestReplaceComponentPreservesOtherComponents(t *testing.T) {
	components := []Component{{Name: "mihomo", Version: "old"}, {Name: "dnsmasq", Version: "stable"}}
	updated := ReplaceComponent(components, Component{Name: "mihomo", Version: "new"})
	if updated[0].Version != "new" || updated[1].Version != "stable" || strings.Join([]string{components[0].Version, components[1].Version}, ",") != "old,stable" {
		t.Fatalf("replacement changed the wrong component: %#v", updated)
	}
}
