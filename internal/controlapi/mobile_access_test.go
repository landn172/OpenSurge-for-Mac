package controlapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMobileAccessSettingsRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	configured, err := store.HasMobileAccessSettings()
	if err != nil || configured {
		t.Fatalf("fresh configured=%v err=%v", configured, err)
	}
	if err := store.SaveMobileAccess(MobileAccessSettings{Enabled: true, Interface: "en0"}); err != nil {
		t.Fatal(err)
	}
	configured, err = store.HasMobileAccessSettings()
	if err != nil || !configured {
		t.Fatalf("saved configured=%v err=%v", configured, err)
	}
	settings, err := store.MobileAccess()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Enabled || settings.Interface != "en0" || settings.SchemaVersion != SchemaVersion {
		t.Fatalf("settings = %#v", settings)
	}
	if err := store.SaveMobileAccess(MobileAccessSettings{Enabled: false, Interface: "en0"}); err != nil {
		t.Fatal(err)
	}
	settings, err = store.MobileAccess()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Enabled || settings.Interface != "" {
		t.Fatalf("disabled settings = %#v", settings)
	}
}

func TestMobileAccessRouteDisablesListenerAndPersistsChoice(t *testing.T) {
	server := mobileServer(t)
	payload := bytes.NewBufferString(`{"enabled":false}`)
	request := httptest.NewRequest(http.MethodPut, "http://127.0.0.1:61767/api/v1/mobile-access", payload)
	request.Host = "127.0.0.1:61767"
	request.Header.Set("Authorization", "Bearer "+server.token)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got MobileAccessResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Enabled || server.MobileAccessEnabled() {
		t.Fatalf("mobile access remained enabled: %#v", got)
	}
	settings, err := server.store.MobileAccess()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Enabled || settings.Interface != "" {
		t.Fatalf("settings = %#v", settings)
	}
}
