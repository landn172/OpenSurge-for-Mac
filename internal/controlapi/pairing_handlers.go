package controlapi

import (
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"
)

// PairingResponse is what the Mac's whitelist page renders as a QR code.
type PairingResponse struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	URL           string    `json:"url"`
	State         string    `json:"state"`
	ExpiresAt     time.Time `json:"expires_at"`
	// AttemptsLeft counts down as wrong codes are entered on the Mac.
	AttemptsLeft int `json:"attempts_left"`
}

type PairedDeviceListResponse struct {
	SchemaVersion int                  `json:"schema_version"`
	Devices       []PairedDevice       `json:"devices"`
	MobileEnabled bool                 `json:"mobile_enabled"`
	PairBaseURL   string               `json:"pair_base_url,omitempty"`
	MobileAccess  MobileAccessResponse `json:"mobile_access"`
}

type MobileAccessRequest struct {
	Enabled   bool   `json:"enabled"`
	Interface string `json:"interface"`
}

type MobileAccessResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Enabled       bool   `json:"enabled"`
	Interface     string `json:"interface,omitempty"`
	Address       string `json:"address,omitempty"`
	BaseURL       string `json:"base_url,omitempty"`
}

// handlePairings creates a pending pairing and returns the URL to render as a
// QR code. Full authority only: starting a pairing is how a new device gets in.
func (s *Server) handlePairings(w http.ResponseWriter, r *http.Request) {
	_, _, baseURL := s.mobileAccessSnapshot()
	if baseURL == "" {
		writeError(w, http.StatusConflict, "mobile_access_disabled", "mobile access is not enabled on this Control Service")
		return
	}
	id := randomToken(24)
	pairing := &pendingPairing{id: id, state: pairingPending, expires: time.Now().Add(pairingTTL)}
	s.mu.Lock()
	s.pruneExpiredPairingsLocked()
	s.pairings[id] = pairing
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, PairingResponse{
		SchemaVersion: SchemaVersion,
		ID:            id,
		URL:           baseURL + "/pair?p=" + id,
		State:         string(pairingPending),
		ExpiresAt:     pairing.expires.UTC(),
		AttemptsLeft:  pairingMaxAttempts,
	})
}

// handlePairingStatus lets the Mac page poll. It never returns the code: the
// code exists precisely so that the operator has to read it off the phone.
func (s *Server) handlePairingStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	pairing, ok := s.pairings[id]
	var response PairingResponse
	if ok {
		response = PairingResponse{
			SchemaVersion: SchemaVersion,
			ID:            id,
			URL:           s.pairURL(id),
			State:         string(pairing.state),
			ExpiresAt:     pairing.expires.UTC(),
			AttemptsLeft:  pairingMaxAttempts - pairing.attempts,
		}
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "pairing_not_found", "pairing is unknown, cancelled or expired")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// handlePairingConfirm completes the binding. The operator types the code shown
// on the phone; a wrong code burns an attempt, and running out destroys the
// pairing rather than allowing indefinite guessing.
func (s *Server) handlePairingConfirm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var request struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &request, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = "未命名设备"
	}
	if len([]rune(name)) > 40 {
		name = string([]rune(name)[:40])
	}

	s.mu.Lock()
	pairing, ok := s.pairings[id]
	if !ok || time.Now().After(pairing.expires) {
		delete(s.pairings, id)
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "pairing_not_found", "pairing is unknown, cancelled or expired")
		return
	}
	if pairing.state != pairingClaimed {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "pairing_not_claimed", "no device has scanned this code yet")
		return
	}
	if !secureEqual(strings.TrimSpace(request.Code), pairing.claimCode) {
		pairing.attempts++
		remaining := pairingMaxAttempts - pairing.attempts
		if remaining <= 0 {
			delete(s.pairings, id)
		}
		s.mu.Unlock()
		if remaining <= 0 {
			writeError(w, http.StatusTooManyRequests, "pairing_abandoned", "too many incorrect codes; start the pairing again")
			return
		}
		writeError(w, http.StatusForbidden, "pair_code_mismatch", fmt.Sprintf("code does not match; %d attempts left", remaining))
		return
	}
	s.mu.Unlock()

	device, token, err := s.devices.add(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "pairing_failed", err.Error())
		return
	}

	s.mu.Lock()
	// Re-read: the pairing could have expired while the registry wrote to disk.
	if current, still := s.pairings[id]; still {
		current.state = pairingCompleted
		current.token = token
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{
		"schema_version": SchemaVersion,
		"device":         PairedDevice{ID: device.ID, Name: device.Name, CreatedAt: device.CreatedAt},
	})
}

func (s *Server) handlePairingCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	delete(s.pairings, id)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePairedDevices(w http.ResponseWriter, r *http.Request) {
	mobileAccess := s.mobileAccessResponse()
	writeJSON(w, http.StatusOK, PairedDeviceListResponse{
		SchemaVersion: SchemaVersion,
		Devices:       s.devices.list(),
		MobileEnabled: mobileAccess.Enabled,
		PairBaseURL:   mobileAccess.BaseURL,
		MobileAccess:  mobileAccess,
	})
}

func (s *Server) pairURL(id string) string {
	_, _, baseURL := s.mobileAccessSnapshot()
	return baseURL + "/pair?p=" + id
}

// handleMobileAccess updates the phone listener from the trusted Mac control
// plane only. A read-only device can never enable exposure or choose an
// interface for itself.
func (s *Server) handleMobileAccess(w http.ResponseWriter, r *http.Request) {
	var request MobileAccessRequest
	if err := decodeJSON(r, &request, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.setMobileAccess(MobileAccessSettings{Enabled: request.Enabled, Interface: request.Interface}); err != nil {
		writeError(w, http.StatusBadRequest, "mobile_access_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.mobileAccessResponse())
}

func (s *Server) handlePairedDeviceRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.devices.revoke(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "device_not_found", "no such paired device")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pruneExpiredPairingsLocked keeps the map from growing without bound. Callers
// hold s.mu.
func (s *Server) pruneExpiredPairingsLocked() {
	now := time.Now()
	for id, pairing := range s.pairings {
		if now.After(pairing.expires) {
			delete(s.pairings, id)
		}
	}
}

// handlePairScan is what the QR points at. It is unauthenticated by necessity
// -- the phone has no credential yet -- so it does the least possible: claim
// the pairing once, hand the phone an opaque claimant handle, and show the code
// that must be typed on the Mac. Scanning alone grants no access to anything.
func (s *Server) handlePairScan(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("p")
	now := time.Now()
	s.mu.Lock()
	pairing, ok := s.pairings[id]
	if ok && now.After(pairing.expires) {
		delete(s.pairings, id)
		ok = false
	}
	var code, claimant string
	alreadyClaimed := false
	if ok {
		switch pairing.state {
		case pairingPending:
			pairing.state = pairingClaimed
			pairing.claimCode = newPairCode()
			pairing.claimant = randomToken(24)
			code, claimant = pairing.claimCode, pairing.claimant
		case pairingClaimed, pairingCompleted:
			// First scanner wins. A second phone must not be shown the code.
			if cookie, err := r.Cookie(pairClaimCookie); err == nil && secureEqual(cookie.Value, pairing.claimant) {
				code, claimant = pairing.claimCode, pairing.claimant
			} else {
				alreadyClaimed = true
			}
		}
	}
	s.mu.Unlock()

	if !ok {
		renderPairPage(w, http.StatusNotFound, pairPageData{Error: "这个配对二维码无效或已过期。请在 Mac 上重新生成。"})
		return
	}
	if alreadyClaimed {
		renderPairPage(w, http.StatusConflict, pairPageData{Error: "这个二维码已经被另一台设备扫过了。请在 Mac 上重新生成一个。"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     pairClaimCookie,
		Value:    claimant,
		Path:     "/",
		Expires:  now.Add(pairingTTL),
		MaxAge:   int(pairingTTL / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	renderPairPage(w, http.StatusOK, pairPageData{Code: code, PairingID: id})
}

// handlePairStatus is polled by the phone while the Mac operator enters the
// code. The final credential handoff deliberately happens through
// handlePairComplete as a top-level browser navigation instead of a fetch
// response. Some mobile in-app browsers do not reliably retain an HttpOnly
// cookie written by a background fetch immediately before a JS redirect.
func (s *Server) handlePairStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("p")
	cookie, err := r.Cookie(pairClaimCookie)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not_claimed", "this browser did not claim the pairing")
		return
	}
	s.mu.Lock()
	pairing, ok := s.pairings[id]
	state := pairingPending
	if ok && secureEqual(cookie.Value, pairing.claimant) {
		state = pairing.state
	} else {
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "pairing_not_found", "pairing is unknown, cancelled or expired")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": SchemaVersion, "state": string(state)})
}

// handlePairComplete hands the device credential to the phone and immediately
// redirects into the SPA. Keeping Set-Cookie and the navigation in one
// top-level response makes the post-pairing login work consistently in mobile
// browser containers as well as Safari and Chrome.
func (s *Server) handlePairComplete(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("p")
	cookie, err := r.Cookie(pairClaimCookie)
	if err != nil {
		renderPairPage(w, http.StatusUnauthorized, pairPageData{Error: "这台浏览器没有配对凭据。请在 Mac 上重新生成二维码后，用当前浏览器扫码。"})
		return
	}

	s.mu.Lock()
	pairing, ok := s.pairings[id]
	if ok && time.Now().After(pairing.expires) {
		delete(s.pairings, id)
		ok = false
	}
	if !ok || !secureEqual(cookie.Value, pairing.claimant) || pairing.state != pairingCompleted || pairing.token == "" {
		s.mu.Unlock()
		renderPairPage(w, http.StatusConflict, pairPageData{Error: "配对尚未完成或已失效。请返回 Mac 上的“已配对设备”页面重新开始。"})
		return
	}
	token := pairing.token
	delete(s.pairings, id)
	s.mu.Unlock()

	now := time.Now()
	http.SetCookie(w, &http.Cookie{
		Name:     deviceCookieName,
		Value:    token,
		Path:     "/",
		Expires:  now.Add(pairedDeviceCookieTTL),
		MaxAge:   int(pairedDeviceCookieTTL / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{Name: pairClaimCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

type pairPageData struct {
	Code      string
	PairingID string
	Error     string
}

// The pairing page is served directly rather than routed through the React
// bundle: it must work before the device has any credential, and keeping it a
// single self-contained document means the phone-side of pairing cannot depend
// on the SPA loading correctly.
var pairPageTemplate = template.Must(template.New("pair").Parse(`<!doctype html>
<html lang="zh-CN"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<title>配对到 OpenSurge</title>
<style>
:root{color-scheme:dark}
body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;
 background:oklch(16.7% .016 180.7);color:oklch(95.4% .010 164.9);
 font-family:-apple-system,BlinkMacSystemFont,"PingFang SC",sans-serif;line-height:1.6}
.card{width:100%;max-width:340px;padding:26px 22px;border:1px solid oklch(30.5% .029 170.4);
 border-radius:18px;background:oklch(20.8% .021 174.1);text-align:center}
h1{margin:0 0 6px;font-size:19px}
p{margin:0;color:oklch(61.4% .027 170.4);font-size:13px}
.code{margin:22px 0 6px;font-size:40px;font-weight:600;letter-spacing:.14em;
 font-variant-numeric:tabular-nums;color:oklch(84.6% .125 161.7)}
.err{color:oklch(67.3% .184 28.4)}
.done{color:oklch(77.8% .180 153.7);font-size:15px;font-weight:600;margin-top:18px}
</style></head><body><div class="card">
{{if .Error}}
  <h1>无法配对</h1><p class="err">{{.Error}}</p>
{{else}}
  <h1>在 Mac 上输入这个配对码</h1>
  <p>打开 Mac 上的「已配对设备」页面，输入下面 6 位数字完成绑定。</p>
  <div class="code">{{.Code}}</div>
  <p>配对码 5 分钟内有效。绑定完成前，这台设备还看不到任何数据。</p>
  <div id="status"></div>
  <script>
  const id={{.PairingID}};
  const poll=async()=>{
    try{
      const r=await fetch('/pair/status?p='+encodeURIComponent(id),{credentials:'same-origin'});
      if(!r.ok){document.getElementById('status').innerHTML='<p class="err">配对已取消或过期。</p>';return}
      const d=await r.json();
      if(d.state==='completed'){
        document.getElementById('status').innerHTML='<div class="done">绑定完成，正在进入…</div>';
        setTimeout(()=>location.replace('/pair/complete?p='+encodeURIComponent(id)),600);return
      }
    }catch(e){}
    setTimeout(poll,1500)
  };poll();
  </script>
{{end}}
</div></body></html>`))

func renderPairPage(w http.ResponseWriter, status int, data pairPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pairPageTemplate.Execute(w, data)
}

func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
