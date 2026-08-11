package controlapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These tests cover the boundary introduced by
// docs/agent-wiki/sources/decisions/control-plane-lan-exposure.md. They are the
// unit-testable half of that decision's validation requirement; the half that
// needs a second real device on the LAN lives in scripts/check-lan-exposure.sh.

const testLANIP = "192.168.1.20"

// mobileServer returns a server with the mobile listener configured. It sets the
// fields directly rather than going through New(), because New() resolves a real
// interface address and there is no interface to resolve in a unit test.
func mobileServer(t *testing.T) *Server {
	t.Helper()
	server := newTestServer(t)
	server.lanAddr = testLANIP + ":61767"
	server.lanBaseURL = "http://" + testLANIP + ":61767"
	return server
}

func withSession(t *testing.T, server *Server, scope sessionScope) *http.Cookie {
	t.Helper()
	value := "session-" + time.Now().Format("150405.000000000")
	server.mu.Lock()
	server.sessions[value] = webSession{expires: time.Now().Add(time.Hour), scope: scope}
	server.mu.Unlock()
	return &http.Cookie{Name: "opensurge_session", Value: value}
}

func noContent() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

func TestReadOnlySessionCannotReachFullAuthorityRoute(t *testing.T) {
	server := mobileServer(t)
	request := httptest.NewRequest(http.MethodPost, "http://"+testLANIP+":61767/api/v1/gateway/start", nil)
	request.Host = testLANIP + ":61767"
	request.Header.Set("Origin", server.lanBaseURL)
	request.AddCookie(withSession(t, server, scopeReadOnly))
	response := httptest.NewRecorder()

	server.auth(noContent()).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("read-only session reached a full-authority route: status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "read_only_session") {
		t.Fatalf("expected read_only_session error, got %s", response.Body.String())
	}
}

func TestReadOnlySessionReachesMobileRoute(t *testing.T) {
	server := mobileServer(t)
	request := httptest.NewRequest(http.MethodGet, "http://"+testLANIP+":61767/api/v1/overview", nil)
	request.Host = testLANIP + ":61767"
	request.AddCookie(withSession(t, server, scopeReadOnly))
	response := httptest.NewRecorder()

	server.authRO(noContent()).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("read-only session was refused a mobile route: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFullSessionReachesBothScopes(t *testing.T) {
	server := mobileServer(t)
	for name, wrap := range map[string]func(http.Handler) http.Handler{"auth": server.auth, "authRO": server.authRO} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:61767/api/v1/overview", nil)
		request.Host = "127.0.0.1:61767"
		request.AddCookie(withSession(t, server, scopeFull))
		response := httptest.NewRecorder()

		wrap(noContent()).ServeHTTP(response, request)

		if response.Code != http.StatusNoContent {
			t.Fatalf("%s refused a full session: status=%d body=%s", name, response.Code, response.Body.String())
		}
	}
}

// The bearer token is the native launcher's credential and never enters a
// browser, so it must keep full authority even on a mobile-enabled server.
func TestBearerTokenKeepsFullAuthority(t *testing.T) {
	server := mobileServer(t)
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:61767/api/v1/gateway/start", nil)
	request.Host = "127.0.0.1:61767"
	request.Header.Set("Authorization", "Bearer "+server.token)
	response := httptest.NewRecorder()

	server.auth(noContent()).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("bearer token lost full authority: status=%d body=%s", response.Code, response.Body.String())
	}
}

// server.go's Host allowlist is the only DNS-rebinding defense in the tree --
// there are no CORS headers anywhere. A request whose Host is an
// attacker-controlled name that resolves to the Mac's LAN address must still be
// rejected after mobile access is enabled.
func TestRebindingHostIsRejectedWithMobileAccessEnabled(t *testing.T) {
	server := mobileServer(t)
	for _, host := range []string{"evil.example.com", "evil.example.com:61767", "attacker.test"} {
		request := httptest.NewRequest(http.MethodGet, "http://"+testLANIP+":61767/api/v1/overview", nil)
		request.Host = host
		response := httptest.NewRecorder()

		server.securityHeaders(noContent()).ServeHTTP(response, request)

		if response.Code != http.StatusForbidden {
			t.Fatalf("rebinding Host %q was accepted: status=%d", host, response.Code)
		}
		if !strings.Contains(response.Body.String(), "invalid_host") {
			t.Fatalf("Host %q: expected invalid_host, got %s", host, response.Body.String())
		}
	}
}

func TestAllowedHostsStayAFixedSet(t *testing.T) {
	loopbackOnly := newTestServer(t)
	if got := loopbackOnly.allowedHosts(); len(got) != 2 || got[0] != "127.0.0.1" || got[1] != "localhost" {
		t.Fatalf("loopback-only server should allow exactly loopback hosts, got %v", got)
	}
	mobile := mobileServer(t)
	got := mobile.allowedHosts()
	if len(got) != 3 || got[2] != testLANIP {
		t.Fatalf("mobile server should add exactly the LAN address, got %v", got)
	}
}

func TestLANHostIsAcceptedWhenMobileAccessEnabled(t *testing.T) {
	server := mobileServer(t)
	request := httptest.NewRequest(http.MethodGet, "http://"+testLANIP+":61767/api/v1/overview", nil)
	request.Host = testLANIP + ":61767"
	response := httptest.NewRecorder()

	server.securityHeaders(noContent()).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("LAN host was rejected: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLANHostIsRejectedWhenMobileAccessDisabled(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "http://"+testLANIP+":61767/api/v1/overview", nil)
	request.Host = testLANIP + ":61767"
	response := httptest.NewRecorder()

	server.securityHeaders(noContent()).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("LAN host was accepted while mobile access is disabled: status=%d", response.Code)
	}
}

func TestForeignOriginIsRejectedForSessionMutations(t *testing.T) {
	server := mobileServer(t)
	request := httptest.NewRequest(http.MethodPost, "http://"+testLANIP+":61767/api/v1/connectivity/tests", nil)
	request.Host = testLANIP + ":61767"
	request.Header.Set("Origin", "http://evil.example.com")
	request.AddCookie(withSession(t, server, scopeReadOnly))
	response := httptest.NewRecorder()

	server.authRO(noContent()).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign origin was accepted: status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "origin_rejected") {
		t.Fatalf("expected origin_rejected, got %s", response.Body.String())
	}
}

func TestLANOriginIsAcceptedForAllowedMobileMutation(t *testing.T) {
	server := mobileServer(t)
	request := httptest.NewRequest(http.MethodPost, "http://"+testLANIP+":61767/api/v1/connectivity/tests", nil)
	request.Host = testLANIP + ":61767"
	request.Header.Set("Origin", server.lanBaseURL)
	request.AddCookie(withSession(t, server, scopeReadOnly))
	response := httptest.NewRecorder()

	server.authRO(noContent()).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("LAN origin was refused for an allowed mobile mutation: status=%d body=%s", response.Code, response.Body.String())
	}
}

// A QR code built on baseURL would encode 127.0.0.1 and point the phone at
// itself. The mobile bootstrap URL must be built on the LAN base URL, and the
// session it grants must be read-only.
func TestMobileBootstrapURLUsesLANBaseAndGrantsReadOnly(t *testing.T) {
	server := mobileServer(t)
	url, err := server.MobileBootstrapURL("devices")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, server.lanBaseURL+"/bootstrap?code=") {
		t.Fatalf("mobile bootstrap URL is not built on the LAN base: %s", url)
	}
	code := strings.TrimPrefix(url, server.lanBaseURL+"/bootstrap?code=")
	server.mu.Lock()
	grant, ok := server.bootstraps[code]
	server.mu.Unlock()
	if !ok {
		t.Fatal("mobile bootstrap code was not recorded")
	}
	if grant.scope != scopeReadOnly {
		t.Fatalf("mobile bootstrap granted scope %v, want read-only", grant.scope)
	}
	if grant.path != "devices" {
		t.Fatalf("mobile bootstrap path=%q", grant.path)
	}
}

func TestMacBootstrapURLStaysLoopbackAndFullScope(t *testing.T) {
	server := mobileServer(t)
	url := server.BootstrapURL()
	if !strings.HasPrefix(url, server.baseURL+"/bootstrap?code=") {
		t.Fatalf("launcher bootstrap URL left loopback: %s", url)
	}
	code := strings.TrimPrefix(url, server.baseURL+"/bootstrap?code=")
	server.mu.Lock()
	grant := server.bootstraps[code]
	server.mu.Unlock()
	if grant.scope != scopeFull {
		t.Fatalf("launcher bootstrap granted scope %v, want full", grant.scope)
	}
}

func TestMobileBootstrapFailsWhenMobileAccessDisabled(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.MobileBootstrapURL("dashboard"); err == nil {
		t.Fatal("mobile bootstrap URL was issued while mobile access is disabled")
	}
}

// The exchanged cookie must carry the grant's scope, otherwise a read-only QR
// would mint a full-authority session.
func TestBootstrapExchangeCarriesGrantScope(t *testing.T) {
	server := mobileServer(t)
	url, err := server.MobileBootstrapURL("dashboard")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimPrefix(url, server.lanBaseURL+"/bootstrap?code=")

	request := httptest.NewRequest(http.MethodGet, "http://"+testLANIP+":61767/bootstrap?code="+code, nil)
	request.Host = testLANIP + ":61767"
	response := httptest.NewRecorder()
	server.exchangeBootstrap(response, request)

	if response.Code != http.StatusFound {
		t.Fatalf("bootstrap exchange status=%d body=%s", response.Code, response.Body.String())
	}
	var session string
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "opensurge_session" {
			session = cookie.Value
		}
	}
	if session == "" {
		t.Fatal("bootstrap exchange issued no session cookie")
	}
	server.mu.Lock()
	got := server.sessions[session]
	server.mu.Unlock()
	if got.scope != scopeReadOnly {
		t.Fatalf("exchanged session scope=%v, want read-only", got.scope)
	}
}

// A session whose scope was never set must get the least authority, so that a
// future code path that forgets to set one fails closed.
func TestZeroValueSessionScopeIsReadOnly(t *testing.T) {
	if scopeReadOnly != 0 {
		t.Fatalf("scopeReadOnly must be the zero value so unset scopes fail closed, got %d", scopeReadOnly)
	}
	var session webSession
	if session.scope != scopeReadOnly {
		t.Fatal("zero-value webSession is not read-only")
	}
}
