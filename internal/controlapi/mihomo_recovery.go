package controlapi

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
)

const (
	mihomoRecoveryIdle                 = "idle"
	mihomoRecoveryObserving            = "observing"
	mihomoRecoveryRecovering           = "recovering"
	mihomoRecoveryFailed               = "failed"
	mihomoFailureProcessMissing        = "process_missing"
	mihomoFailureControllerRefused     = "controller_refused"
	autoMihomoRecoveryInterval         = 5 * time.Second
	mihomoRecoveryHealthyConfirmations = 2
)

// mihomoRecoveryController allows one automatic restart per incident. A
// successful command alone is not health evidence: two later healthy status
// samples are required before another incident can be considered.
type mihomoRecoveryController struct {
	mu                         sync.Mutex
	state, reason, error       string
	refusedCount, healthyCount int
	attempted, operationActive bool
}

func newMihomoRecoveryController() *mihomoRecoveryController {
	return &mihomoRecoveryController{state: mihomoRecoveryIdle}
}
func (c *mihomoRecoveryController) snapshot() MihomoRecoveryStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return MihomoRecoveryStatus{State: c.state, Reason: c.reason, Error: c.error}
}
func (c *mihomoRecoveryController) observeUnknown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.operationActive {
		c.healthyCount, c.refusedCount = 0, 0
	}
}
func (c *mihomoRecoveryController) observeHealthy() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operationActive {
		return
	}
	if c.attempted {
		c.healthyCount++
		c.state, c.error = mihomoRecoveryRecovering, ""
		if c.healthyCount < mihomoRecoveryHealthyConfirmations {
			return
		}
	}
	c.state, c.reason, c.error, c.refusedCount, c.healthyCount, c.attempted = mihomoRecoveryIdle, "", "", 0, 0, false
}
func (c *mihomoRecoveryController) observeFailure(reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operationActive {
		return false
	}
	c.healthyCount = 0
	if c.attempted && c.state == mihomoRecoveryRecovering {
		c.state, c.reason, c.error = mihomoRecoveryFailed, reason, "mihomo remained unhealthy after restart"
		return false
	}
	if c.attempted || c.state == mihomoRecoveryFailed {
		return false
	}
	if c.reason != reason {
		c.reason, c.refusedCount = reason, 0
	}
	c.state, c.error = mihomoRecoveryObserving, ""
	if reason == mihomoFailureProcessMissing {
		return true
	}
	if reason == mihomoFailureControllerRefused {
		c.refusedCount++
		return c.refusedCount >= 2
	}
	return false
}
func (c *mihomoRecoveryController) begin(reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attempted || c.state == mihomoRecoveryRecovering || c.state == mihomoRecoveryFailed {
		return false
	}
	c.state, c.reason, c.error, c.attempted, c.healthyCount, c.operationActive = mihomoRecoveryRecovering, reason, "", true, 0, true
	return true
}
func (c *mihomoRecoveryController) finishAutomatic(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.operationActive = false
	if err != nil {
		c.state, c.error = mihomoRecoveryFailed, err.Error()
		return
	}
	c.state, c.error = mihomoRecoveryRecovering, ""
}

func (s *Server) monitorMihomoRecovery(ctx context.Context) {
	s.evaluateMihomoRecovery(ctx)
	ticker := time.NewTicker(autoMihomoRecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.evaluateMihomoRecovery(ctx)
		}
	}
}
func (s *Server) evaluateMihomoRecovery(ctx context.Context) {
	cfg, err := config.LoadRuntime(s.configPath)
	if err != nil {
		s.mihomoRecovery.observeUnknown()
		return
	}
	status, err := gateway.New(cfg).Status(ctx)
	if err != nil || status.RuntimeState != "active" {
		s.mihomoRecovery.observeUnknown()
		return
	}
	reason := mihomoFailureReason(status)
	if reason == "" {
		s.mihomoRecovery.observeHealthy()
		return
	}
	recovery, err := s.store.Recovery()
	if err != nil || !mihomoRecoveryStageAllowed(cfg.Gateway.Mode, recovery.Stage) {
		s.mihomoRecovery.observeUnknown()
		return
	}
	if !s.mihomoRecovery.observeFailure(reason) || !s.lifecycleMu.TryLock() {
		return
	}
	if !s.mihomoRecovery.begin(reason) {
		s.lifecycleMu.Unlock()
		return
	}
	now := time.Now().UTC()
	op := Operation{SchemaVersion: SchemaVersion, ID: "auto-restart-mihomo-" + randomToken(8), Kind: "restart-mihomo", State: "running", CreatedAt: now, UpdatedAt: now}
	if err := s.store.SaveOperation(op); err != nil {
		s.lifecycleMu.Unlock()
		s.mihomoRecovery.finishAutomatic(err)
		return
	}
	go func() {
		defer s.lifecycleMu.Unlock()
		s.runOperationCore(op, cfg.Gateway.Mode, recovery)
		latest, err := s.store.Operation(op.ID)
		if err != nil {
			s.mihomoRecovery.finishAutomatic(err)
			return
		}
		if latest.State == "failed" {
			s.mihomoRecovery.finishAutomatic(errors.New(latest.Error))
			return
		}
		s.mihomoRecovery.finishAutomatic(nil)
	}()
}
func mihomoFailureReason(status gateway.Status) string {
	if !strings.HasPrefix(status.Mihomo, "running") {
		return mihomoFailureProcessMissing
	}
	if connectionRefused(status.MihomoError) || connectionRefused(status.TUNError) {
		return mihomoFailureControllerRefused
	}
	return ""
}
func connectionRefused(value string) bool {
	return strings.Contains(strings.ToLower(value), "connection refused")
}
func mihomoRecoveryStageAllowed(topology, stage string) bool {
	return topology != config.GatewayModeSameWiFiDHCP || stage == RecoveryGatewayActive || stage == RecoveryClientValidated || stage == RecoveryClientValidationSkipped
}
