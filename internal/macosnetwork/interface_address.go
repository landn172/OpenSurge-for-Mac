package macosnetwork

import (
	"fmt"
	"net"
)

// InterfaceIPv4 returns the interface's current unicast IPv4 address.
//
// The upstream interface's address is handed out by the router's DHCP and
// changes, so it cannot be baked into the launchd plist at install time. The
// Control Service resolves it by interface name every time it starts.
//
// Loopback, link-local (169.254/16) and multicast addresses are rejected: an
// interface that only holds those is not carrying a usable LAN address, and
// binding one of them would produce a listener no phone can reach.
func InterfaceIPv4(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("interface name is required")
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return "", fmt.Errorf("look up interface %s: %w", name, err)
	}
	if iface.Flags&net.FlagUp == 0 {
		return "", fmt.Errorf("interface %s is down", name)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("read addresses of interface %s: %w", name, err)
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			continue
		}
		return ip.String(), nil
	}
	return "", fmt.Errorf("interface %s has no usable IPv4 address", name)
}
