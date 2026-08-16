package controlapi

import (
	"testing"

	"open-mihomo-gateway/internal/macosnetwork"
)

func TestSuggestDHCPRange24AvoidsGatewayRouterAndProtectedAddresses(t *testing.T) {
	start, end, err := suggestDHCPRange24(macosnetwork.Snapshot{IPv4: "192.168.7.100", SubnetMask: "255.255.255.0", Router: "192.168.7.1"}, []string{"192.168.7.101", "192.168.7.102"})
	if err != nil {
		t.Fatal(err)
	}
	if start != "192.168.7.103" || end != "192.168.7.200" {
		t.Fatalf("range = %s-%s", start, end)
	}
}

func TestSuggestDHCPRange24RejectsNon24Network(t *testing.T) {
	_, _, err := suggestDHCPRange24(macosnetwork.Snapshot{IPv4: "10.0.0.2", SubnetMask: "255.255.0.0", Router: "10.0.0.1"}, nil)
	if err == nil {
		t.Fatal("expected non-/24 network to be rejected")
	}
}
