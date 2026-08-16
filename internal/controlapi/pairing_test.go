package controlapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// startPairing runs the Mac-side "pair a new device" action.
func startPairing(t *testing.T, server *Server) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:61767/api/v1/pairings", nil)
	request.Host = "127.0.0.1:61767"
	response := httptest.NewRecorder()
	server.handlePairings(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("start pairing status=%d body=%s", response.Code, response.Body.String())
	}
	var payload PairingResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(payload.URL, server.lanBaseURL+"/pair?p=") {
		t.Fatalf("pairing URL is not phone-reachable: %s", payload.URL)
	}
	return payload.ID
}

// scan runs the phone-side scan and returns the claim cookie plus the code the
// phone displays.
func scan(t *testing.T, server *Server, id string, cookies ...*http.Cookie) (*http.Cookie, string, int) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://"+testLANIP+":61767/pair?p="+id, nil)
	request.Host = testLANIP + ":61767"
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	server.handlePairScan(response, request)

	var claim *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == pairClaimCookie && cookie.Value != "" {
			claim = cookie
		}
	}
	code := ""
	if server != nil && id != "" {
		server.mu.Lock()
		if pairing, ok := server.pairings[id]; ok {
			code = pairing.claimCode
		}
		server.mu.Unlock()
	}
	return claim, code, response.Code
}

func confirm(t *testing.T, server *Server, id, code, name string) *httptest.ResponseRecorder {
	t.Helper()
	body := strings.NewReader(`{"code":"` + code + `","name":"` + name + `"}`)
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:61767/api/v1/pairings/"+id+"/confirm", body)
	request.Host = "127.0.0.1:61767"
	request.SetPathValue("id", id)
	response := httptest.NewRecorder()
	server.handlePairingConfirm(response, request)
	return response
}

func pollPairStatus(t *testing.T, server *Server, id string, claim *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://"+testLANIP+":61767/pair/status?p="+id, nil)
	request.Host = testLANIP + ":61767"
	if claim != nil {
		request.AddCookie(claim)
	}
	response := httptest.NewRecorder()
	server.handlePairStatus(response, request)
	return response
}

func TestPairingHappyPathBindsARevocableDevice(t *testing.T) {
	server := mobileServer(t)
	id := startPairing(t, server)

	claim, code, status := scan(t, server, id)
	if status != http.StatusOK || claim == nil || code == "" {
		t.Fatalf("scan failed: status=%d claim=%v code=%q", status, claim, code)
	}
	if len(code) != 6 {
		t.Fatalf("pair code should be 6 digits, got %q", code)
	}

	// Before confirmation the phone has nothing.
	if got := pollPairStatus(t, server, id, claim); !strings.Contains(got.Body.String(), `"claimed"`) {
		t.Fatalf("expected claimed state before confirmation, got %s", got.Body.String())
	}

	if response := confirm(t, server, id, code, "我的 iPhone"); response.Code != http.StatusCreated {
		t.Fatalf("confirm status=%d body=%s", response.Code, response.Body.String())
	}

	// The phone observes completion, then performs a top-level navigation which
	// sets its persistent device credential and enters the SPA.
	response := pollPairStatus(t, server, id, claim)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"completed"`) {
		t.Fatalf("phone did not observe completion: status=%d body=%s", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == deviceCookieName {
			t.Fatal("status polling must not issue the device cookie")
		}
	}
	completeRequest := httptest.NewRequest(http.MethodGet, "http://"+testLANIP+":61767/pair/complete?p="+id, nil)
	completeRequest.Host = testLANIP + ":61767"
	completeRequest.AddCookie(claim)
	complete := httptest.NewRecorder()
	server.handlePairComplete(complete, completeRequest)
	if complete.Code != http.StatusFound || complete.Header().Get("Location") != "/dashboard" {
		t.Fatalf("pair completion redirect status=%d location=%q body=%s", complete.Code, complete.Header().Get("Location"), complete.Body.String())
	}
	var deviceToken string
	for _, cookie := range complete.Result().Cookies() {
		if cookie.Name == deviceCookieName && cookie.Value != "" {
			deviceToken = cookie.Value
		}
	}
	if deviceToken == "" {
		t.Fatal("no device cookie issued to the phone")
	}

	// The whitelist shows it, by name, without leaking the token hash.
	devices := server.devices.list()
	if len(devices) != 1 || devices[0].Name != "我的 iPhone" {
		t.Fatalf("whitelist=%+v", devices)
	}
	if devices[0].TokenHash != "" {
		t.Fatal("token hash leaked out of the registry")
	}

	// The device token authenticates, read-only.
	request := httptest.NewRequest(http.MethodGet, "http://"+testLANIP+":61767/api/v1/overview", nil)
	request.Host = testLANIP + ":61767"
	request.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	recorder := httptest.NewRecorder()
	server.authRO(noContent()).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("paired device could not read: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "http://"+testLANIP+":61767/api/v1/gateway/start", nil)
	request.Host = testLANIP + ":61767"
	request.Header.Set("Origin", server.lanBaseURL)
	request.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	server.auth(noContent()).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("paired device reached a full-authority route: status=%d", recorder.Code)
	}

	// Revoking takes effect on the next request, not on some cache expiry.
	if !server.devices.revoke(devices[0].ID) {
		t.Fatal("revoke reported failure")
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "http://"+testLANIP+":61767/api/v1/overview", nil)
	request.Host = testLANIP + ":61767"
	request.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	server.authRO(noContent()).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("revoked device still authenticated: status=%d", recorder.Code)
	}
}

// Scanning is not authorization. Someone who photographs the QR gets the code
// on their own screen and nothing else -- the binding needs the code typed on
// the Mac.
func TestScanAloneGrantsNoAccess(t *testing.T) {
	server := mobileServer(t)
	id := startPairing(t, server)
	claim, _, _ := scan(t, server, id)
	if claim == nil {
		t.Fatal("scan issued no claim cookie")
	}

	request := httptest.NewRequest(http.MethodGet, "http://"+testLANIP+":61767/api/v1/overview", nil)
	request.Host = testLANIP + ":61767"
	request.AddCookie(claim)
	response := httptest.NewRecorder()
	server.authRO(noContent()).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("a claim cookie alone authenticated: status=%d body=%s", response.Code, response.Body.String())
	}
	if len(server.devices.list()) != 0 {
		t.Fatal("scanning alone created a whitelist entry")
	}
}

func TestSecondScannerIsRefusedTheCode(t *testing.T) {
	server := mobileServer(t)
	id := startPairing(t, server)
	first, code, _ := scan(t, server, id)
	if first == nil || code == "" {
		t.Fatal("first scan failed")
	}
	_, _, status := scan(t, server, id) // no claim cookie: a different phone
	if status != http.StatusConflict {
		t.Fatalf("second scanner was not refused: status=%d", status)
	}
}

func TestWrongPairCodeIsAttemptLimited(t *testing.T) {
	server := mobileServer(t)
	id := startPairing(t, server)
	if _, code, _ := scan(t, server, id); code == "" {
		t.Fatal("scan failed")
	}

	for attempt := 1; attempt < pairingMaxAttempts; attempt++ {
		response := confirm(t, server, id, "000000", "x")
		// A correct-by-accident guess would break the test; 000000 colliding is
		// a 1-in-10^6 event, and the assertion below tolerates it explicitly.
		if response.Code == http.StatusCreated {
			t.Skip("randomly generated code collided with the guess")
		}
		if response.Code != http.StatusForbidden {
			t.Fatalf("attempt %d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
	}
	response := confirm(t, server, id, "000000", "x")
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("pairing was not abandoned after %d wrong codes: status=%d", pairingMaxAttempts, response.Code)
	}
	if len(server.devices.list()) != 0 {
		t.Fatal("a device was bound despite wrong codes")
	}
	// The pairing is gone, so a later correct code cannot resurrect it.
	if response := confirm(t, server, id, "000000", "x"); response.Code != http.StatusNotFound {
		t.Fatalf("abandoned pairing still answers: status=%d", response.Code)
	}
}

func TestConfirmBeforeScanIsRefused(t *testing.T) {
	server := mobileServer(t)
	id := startPairing(t, server)
	if response := confirm(t, server, id, "123456", "x"); response.Code != http.StatusConflict {
		t.Fatalf("confirm succeeded before any scan: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestExpiredPairingCannotBeCompleted(t *testing.T) {
	server := mobileServer(t)
	id := startPairing(t, server)
	_, code, _ := scan(t, server, id)

	server.mu.Lock()
	server.pairings[id].expires = time.Now().Add(-time.Second)
	server.mu.Unlock()

	if response := confirm(t, server, id, code, "x"); response.Code != http.StatusNotFound {
		t.Fatalf("expired pairing was completed: status=%d", response.Code)
	}
}

func TestPairingRequiresMobileAccess(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:61767/api/v1/pairings", nil)
	response := httptest.NewRecorder()
	server.handlePairings(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("pairing was allowed while mobile access is disabled: status=%d", response.Code)
	}
}

// The whitelist is a credential store, so it must persist the hash and never
// the token itself.
func TestRegistryPersistsOnlyHashedTokens(t *testing.T) {
	server := mobileServer(t)
	_, token, err := server.devices.add("phone")
	if err != nil {
		t.Fatal(err)
	}
	data, err := readFileString(server.devices.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(data, token) {
		t.Fatal("the raw device token was written to disk")
	}
	if !strings.Contains(data, hashDeviceToken(token)) {
		t.Fatal("the token hash was not persisted")
	}
}

func TestRegistrySurvivesReload(t *testing.T) {
	server := mobileServer(t)
	device, token, err := server.devices.add("phone")
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := newDeviceRegistry(server.store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.lookup(token)
	if !ok {
		t.Fatal("paired device did not survive a reload")
	}
	if got.ID != device.ID || got.Name != "phone" {
		t.Fatalf("reloaded device=%+v", got)
	}
}

func readFileString(path string) (string, error) {
	data, err := os.ReadFile(path)
	return string(data), err
}
