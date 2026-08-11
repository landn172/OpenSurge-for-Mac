package macosnetwork

import (
	"net"
	"strings"
	"testing"
)

func TestInterfaceIPv4RequiresAName(t *testing.T) {
	if _, err := InterfaceIPv4(""); err == nil {
		t.Fatal("empty interface name was accepted")
	}
}

func TestInterfaceIPv4RejectsUnknownInterface(t *testing.T) {
	if _, err := InterfaceIPv4("nonexistent-interface-zz0"); err == nil {
		t.Fatal("unknown interface was accepted")
	}
}

// The loopback interface has an address and is up, so it exercises the filter
// rather than the lookup: binding 127.0.0.1 here would produce a listener no
// phone can reach, which is worse than failing loudly.
func TestInterfaceIPv4RejectsLoopbackOnlyInterface(t *testing.T) {
	name := loopbackInterfaceName(t)
	address, err := InterfaceIPv4(name)
	if err == nil {
		t.Fatalf("loopback interface %s yielded a bindable address %q", name, address)
	}
	if !strings.Contains(err.Error(), "no usable IPv4") {
		t.Fatalf("unexpected error for loopback interface: %v", err)
	}
}

// Whatever this host's real interfaces are, any address the helper returns must
// be a parseable, non-loopback, non-link-local IPv4.
func TestInterfaceIPv4ReturnsOnlyUsableAddresses(t *testing.T) {
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("cannot enumerate interfaces: %v", err)
	}
	checked := 0
	for _, iface := range interfaces {
		address, err := InterfaceIPv4(iface.Name)
		if err != nil {
			continue
		}
		checked++
		ip := net.ParseIP(address)
		if ip == nil {
			t.Fatalf("%s returned unparseable address %q", iface.Name, address)
		}
		if ip.To4() == nil {
			t.Fatalf("%s returned a non-IPv4 address %q", iface.Name, address)
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			t.Fatalf("%s returned unusable address %q", iface.Name, address)
		}
	}
	if checked == 0 {
		t.Skip("no interface on this host has a usable IPv4 address")
	}
}

func loopbackInterfaceName(t *testing.T) string {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("cannot enumerate interfaces: %v", err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 && iface.Flags&net.FlagUp != 0 {
			return iface.Name
		}
	}
	t.Skip("no loopback interface is up on this host")
	return ""
}
