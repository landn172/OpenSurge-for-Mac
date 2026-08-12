package controlapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Device pairing for the mobile (H5) surface.
//
// A paired device is a named, persistent, revocable identity. That is the
// difference that matters: an anonymous session cannot be listed, attributed,
// or thrown away, so "which phones can reach my gateway" would be a question
// with no answer.
//
// The QR code is a bearer secret -- anyone who photographs the screen or sees a
// screen share holds it. So scanning alone must not grant access. The scan only
// *claims* a pending pairing; the phone then shows a short code that must be
// typed on the Mac to complete the binding. Completing therefore requires
// control of both screens, and the adjudication happens on the Mac, where the
// whitelist lives.

const (
	pairingTTL          = 5 * time.Minute
	pairingMaxAttempts  = 5
	pairedDeviceFile    = "paired-devices.json"
	pairedDeviceVersion = 1
)

type pairingState string

const (
	pairingPending   pairingState = "pending"   // QR shown, nothing scanned yet
	pairingClaimed   pairingState = "claimed"   // a phone scanned; code is on the phone
	pairingCompleted pairingState = "completed" // code confirmed on the Mac; device bound
)

type pendingPairing struct {
	id        string
	state     pairingState
	expires   time.Time
	claimCode string // shown on the phone, typed on the Mac
	claimant  string // opaque handle held by the claiming phone
	attempts  int
	// token is set once the Mac confirms, and collected by the phone on its
	// next poll. It is never returned to the Mac.
	token string
}

// PairedDevice is the persisted whitelist entry. It deliberately holds a hash
// of the device token, never the token: the file is a credential store, and a
// leaked copy must not be usable.
type PairedDevice struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	TokenHash  string    `json:"token_hash"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at,omitempty"`
	LastIP     string    `json:"last_ip,omitempty"`
}

type pairedDeviceDocument struct {
	SchemaVersion int            `json:"schema_version"`
	Devices       []PairedDevice `json:"devices"`
}

// deviceRegistry is the whitelist. It is kept in memory and mirrored to disk so
// that revocation takes effect on the very next request rather than whenever a
// cache happens to expire.
type deviceRegistry struct {
	path    string
	mu      sync.Mutex
	devices map[string]PairedDevice // keyed by token hash
}

func newDeviceRegistry(storeDir string) (*deviceRegistry, error) {
	registry := &deviceRegistry{
		path:    filepath.Join(storeDir, pairedDeviceFile),
		devices: map[string]PairedDevice{},
	}
	if err := registry.load(); err != nil {
		return nil, err
	}
	return registry, nil
}

func (r *deviceRegistry) load() error {
	data, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read paired devices: %w", err)
	}
	var document pairedDeviceDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("decode paired devices: %w", err)
	}
	if document.SchemaVersion != pairedDeviceVersion {
		return fmt.Errorf("unsupported paired device schema version %d", document.SchemaVersion)
	}
	for _, device := range document.Devices {
		if device.TokenHash == "" {
			continue
		}
		r.devices[device.TokenHash] = device
	}
	return nil
}

// saveLocked persists the whitelist. Callers hold r.mu.
func (r *deviceRegistry) saveLocked() error {
	document := pairedDeviceDocument{SchemaVersion: pairedDeviceVersion, Devices: []PairedDevice{}}
	for _, device := range r.devices {
		document.Devices = append(document.Devices, device)
	}
	sortDevicesByCreation(document.Devices)
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(r.path, append(data, '\n'), 0o600)
}

func (r *deviceRegistry) list() []PairedDevice {
	r.mu.Lock()
	defer r.mu.Unlock()
	devices := make([]PairedDevice, 0, len(r.devices))
	for _, device := range r.devices {
		device.TokenHash = "" // never leaves the process
		devices = append(devices, device)
	}
	sortDevicesByCreation(devices)
	return devices
}

func (r *deviceRegistry) add(name string) (PairedDevice, string, error) {
	token := randomToken(32)
	device := PairedDevice{
		ID:        randomToken(8),
		Name:      name,
		TokenHash: hashDeviceToken(token),
		CreatedAt: time.Now().UTC(),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.devices[device.TokenHash] = device
	if err := r.saveLocked(); err != nil {
		delete(r.devices, device.TokenHash)
		return PairedDevice{}, "", err
	}
	return device, token, nil
}

// lookup resolves a device token to its whitelist entry. A revoked device is
// simply absent, so its token stops working on the next request.
func (r *deviceRegistry) lookup(token string) (PairedDevice, bool) {
	if token == "" {
		return PairedDevice{}, false
	}
	hash := hashDeviceToken(token)
	r.mu.Lock()
	defer r.mu.Unlock()
	device, ok := r.devices[hash]
	return device, ok
}

func (r *deviceRegistry) touch(token, remoteIP string) {
	hash := hashDeviceToken(token)
	r.mu.Lock()
	defer r.mu.Unlock()
	device, ok := r.devices[hash]
	if !ok {
		return
	}
	now := time.Now().UTC()
	// Persisting on every request would mean a disk write per poll. Once a
	// minute is enough for a "last seen" column and keeps the file quiet.
	if now.Sub(device.LastSeenAt) < time.Minute && device.LastIP == remoteIP {
		return
	}
	device.LastSeenAt = now
	device.LastIP = remoteIP
	r.devices[hash] = device
	_ = r.saveLocked()
}

func (r *deviceRegistry) revoke(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for hash, device := range r.devices {
		if device.ID == id {
			delete(r.devices, hash)
			if err := r.saveLocked(); err != nil {
				r.devices[hash] = device
				return false
			}
			return true
		}
	}
	return false
}

func hashDeviceToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func sortDevicesByCreation(devices []PairedDevice) {
	for i := 1; i < len(devices); i++ {
		for j := i; j > 0 && devices[j].CreatedAt.Before(devices[j-1].CreatedAt); j-- {
			devices[j], devices[j-1] = devices[j-1], devices[j]
		}
	}
}

// newPairCode returns a 6-digit code. Six digits is only 10^6, which is why
// confirmation is attempt-limited: the code's job is to prove the operator sees
// the phone's screen, not to withstand offline guessing.
func newPairCode() string {
	limit := big.NewInt(1000000)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%06d", n.Int64())
}
