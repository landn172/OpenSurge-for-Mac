package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/device"
)

// serverWithDevicePolicy builds a control plane whose desired device policy is
// whatever the caller writes, so a document the gateway LAN cannot accept can
// be exercised.
func serverWithDevicePolicy(t *testing.T, policy string) *Server {
	t.Helper()
	dir := t.TempDir()
	mihomoAPI := newReadyMihomoTestServer(t)
	configPath := filepath.Join(dir, "config.yaml")
	policyPath := filepath.Join(dir, "device-policy.json")
	if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`gateway:
  mode: "same_wifi_dhcp"
  interface: "en0"
  lan_ip: "192.168.1.20"
  upstream_interface: "en0"
dhcp:
  enabled: true
  range_start: "192.168.1.120"
  range_end: "192.168.1.199"
device_policy:
  file: %q
transparent:
  mode: "tun"
mihomo:
  api_addr: %q
runtime:
  dir: %q
`, policyPath, mihomoAPI.URL, filepath.Join(dir, "runtime"))), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{
		ConfigPath: configPath, Addr: "127.0.0.1:61767",
		StoreDir: filepath.Join(dir, "store"), Runner: fakeRunner{},
		Static: http.NotFoundHandler(), Credentials: &memoryCredentialStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

const emptyDevicePolicy = `{"devices":[],"profiles":[],"templates":[],"rule_sets":[]}`

// A device outside the gateway LAN is structurally valid -- it parses, compiles
// and produces a digest -- but the configuration refuses it, because that check
// needs the gateway LAN the document says nothing about.
const devicePolicyOutsideTheGatewayLAN = `{
  "devices":[{"id":"phone","name":"phone","mac":"aa:bb:cc:dd:ee:ff","ipv4":"10.0.0.5","profile":"phone-policy","egress_mode":"inherit_global"}],
  "profiles":[{"id":"phone-policy","default_policies":["DIRECT"],"on_unsupported":"reject"}],
  "templates":[],
  "rule_sets":[]
}`

func TestOverviewAndStateEventReportTheSameDrift(t *testing.T) {
	server := serverWithDevicePolicy(t, emptyDevicePolicy)

	overview, err := server.overview(context.Background())
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	event, err := server.stateEvent(context.Background())
	if err != nil {
		t.Fatalf("state event: %v", err)
	}

	if overview.Drift != event.Drift {
		t.Errorf("drift: overview=%v event=%v", overview.Drift, event.Drift)
	}
	if overview.DesiredDigest != event.DesiredDigest {
		t.Errorf("desired digest: overview=%q event=%q", overview.DesiredDigest, event.DesiredDigest)
	}
	if overview.AppliedDigest != event.AppliedDigest {
		t.Errorf("applied digest: overview=%q event=%q", overview.AppliedDigest, event.AppliedDigest)
	}
	if overview.DesiredProfileDigest != event.DesiredProfileDigest {
		t.Errorf("desired profile digest: overview=%q event=%q", overview.DesiredProfileDigest, event.DesiredProfileDigest)
	}
	if overview.Revision != event.Revision {
		t.Errorf("revision: overview=%q event=%q", overview.Revision, event.Revision)
	}
}

// The dashboard and the event stream must not disagree about whether a desired
// document the gateway LAN rejects counts as drift.
func TestDriftAgreesWhenTheDesiredPolicyIsRejectedByTheGatewayLAN(t *testing.T) {
	server := serverWithDevicePolicy(t, devicePolicyOutsideTheGatewayLAN)

	// The precondition that used to split the two paths: reading the document
	// directly succeeds, loading it as configuration does not.
	if _, err := device.LoadPolicyBundle(filepath.Join(filepath.Dir(server.configPath), "device-policy.json")); err != nil {
		t.Fatalf("the document must still parse and compile on its own: %v", err)
	}
	if _, err := config.Load(server.configPath); err == nil {
		t.Fatal("the gateway LAN must reject this document as configuration")
	}

	overview, err := server.overview(context.Background())
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	event, err := server.stateEvent(context.Background())
	if err != nil {
		t.Fatalf("state event: %v", err)
	}

	if overview.DesiredDigest != event.DesiredDigest {
		t.Errorf("desired digest: overview=%q event=%q", overview.DesiredDigest, event.DesiredDigest)
	}
	if overview.Drift != event.Drift {
		t.Errorf("drift: overview=%v event=%v", overview.Drift, event.Drift)
	}
	if event.DesiredDigest != "" {
		t.Errorf("a document the topology cannot apply must not report a desired digest, got %q", event.DesiredDigest)
	}
	if len(overview.Warnings) == 0 {
		t.Error("the rejected desired configuration must reach the operator as a warning")
	}
}
