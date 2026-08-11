package controlapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// mobileMutations is the complete set of state-changing routes a read-only
// phone session may reach. It is deliberately tiny, and it is the enumeration
// docs/agent-wiki/sources/decisions/control-plane-lan-exposure.md requires be
// re-proven whenever a route is added.
//
// Adding an entry here is a security decision, not a formality: everything
// listed is reachable by whoever holds the phone, over plaintext HTTP, on the
// segment the gateway serves. Switching an outlet is recoverable; applying a
// mihomo profile or starting a DHCP takeover is not.
//
// These three are not equally scoped. Device selector and connectivity test
// affect one device and nothing respectively. Policy group selection is
// household-wide by design: it changes egress for everything routed by that
// group, choosing among proxies already present in the applied profile. That
// is intended -- it is the "switch the Selector" the surface exists for -- but
// do not read the three as uniformly device-scoped.
var mobileMutations = map[string]bool{
	"POST /api/v1/devices/{device}/selectors/{slot}": true,
	"POST /api/v1/policies/{group}/selection":        true,
	"POST /api/v1/connectivity/tests":                true,
}

// TestMobileSurfaceExposesNoUndeclaredWriteRoute reads the route table out of
// the source rather than the running mux, because net/http's ServeMux does not
// expose its registered patterns. A new mutating route marked s.authRO fails
// here until somebody adds it to mobileMutations on purpose.
func TestMobileSurfaceExposesNoUndeclaredWriteRoute(t *testing.T) {
	readOnly, full := parseRouteTable(t)

	if len(readOnly) == 0 || len(full) == 0 {
		t.Fatalf("route table parse produced nothing usable: %d read-only, %d full", len(readOnly), len(full))
	}

	var undeclared []string
	for _, route := range readOnly {
		method, _, found := strings.Cut(route, " ")
		if !found {
			t.Fatalf("route %q has no method", route)
		}
		if method == "GET" || method == "HEAD" {
			continue
		}
		if !mobileMutations[route] {
			undeclared = append(undeclared, route)
		}
	}
	sort.Strings(undeclared)
	if len(undeclared) > 0 {
		t.Fatalf("these mutating routes are reachable from a read-only phone session but are not declared in mobileMutations:\n  %s\n\n"+
			"Either wrap them in s.auth (full authority only), or add them to mobileMutations after deciding that a phone on the LAN may perform them.",
			strings.Join(undeclared, "\n  "))
	}
}

// The dangerous routes must stay full-authority. This is stated positively so
// the test fails loudly if one is ever flipped to authRO, instead of relying on
// the allowlist check above to catch it indirectly.
func TestHighBlastRadiusRoutesStayFullAuthority(t *testing.T) {
	readOnly, full := parseRouteTable(t)
	fullSet := map[string]bool{}
	for _, route := range full {
		fullSet[route] = true
	}
	readOnlySet := map[string]bool{}
	for _, route := range readOnly {
		readOnlySet[route] = true
	}

	// Tier 1 is attacker-supplied mihomo profile apply; the rest rewrite the
	// network or drive the gateway data plane.
	mustBeFull := []string{
		"POST /api/v1/sources",
		"POST /api/v1/sources/{id}/apply",
		"POST /api/v1/sources/{id}/refresh",
		"PUT /api/v1/config",
		"PUT /api/v1/device-policy",
		"POST /api/v1/gateway/start",
		"POST /api/v1/gateway/stop",
		"POST /api/v1/gateway/reload",
		"POST /api/v1/gateway/restart-mihomo",
		"POST /api/v1/network/apply-static",
		"POST /api/v1/network/restore-dhcp",
		"POST /api/v1/network/dhcp-probe",
		"POST /api/v1/local-routing",
		"POST /api/v1/recovery",
	}
	for _, route := range mustBeFull {
		if readOnlySet[route] {
			t.Errorf("%s is reachable from a read-only phone session; it must stay full authority", route)
			continue
		}
		if !fullSet[route] {
			t.Errorf("%s is not registered as a full-authority route; did the route change name?", route)
		}
	}
}

// parseRouteTable returns the patterns registered via s.authRO(...) and
// s.auth(...) inside Handler().
func parseRouteTable(t *testing.T) (readOnly, full []string) {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Handle" {
			return true
		}
		if ident, ok := selector.X.(*ast.Ident); !ok || ident.Name != "mux" {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		pattern, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		wrapper, ok := call.Args[1].(*ast.CallExpr)
		if !ok {
			return true
		}
		wrapperSelector, ok := wrapper.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch wrapperSelector.Sel.Name {
		case "authRO":
			readOnly = append(readOnly, pattern)
		case "auth":
			full = append(full, pattern)
		}
		return true
	})
	return readOnly, full
}
