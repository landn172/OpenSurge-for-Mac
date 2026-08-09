package controlapi

import (
	"context"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
	"open-mihomo-gateway/internal/runtime"
)

// gatewayState is the core every control-plane view shares: the loaded
// configuration, the gateway status, the four digests that define drift, and
// the recovery state. Overview, MenuBarStatus and StateEvent are all
// projections of it, so drift has one definition rather than one per view.
//
// It is deliberately cheap. /events rebuilds it every two seconds, so nothing
// here may run doctor checks or call mihomo; that enrichment belongs to the
// Overview projection, which is only built on request.
type gatewayState struct {
	cfg config.Config
	// configErr is why the desired configuration could not be loaded, when the
	// runtime configuration was used instead. The desired device-policy bundle
	// is absent in that case, which is what makes an unpreparable document
	// surface as drift.
	configErr error
	// runtimeErr is set when even the runtime configuration could not be
	// loaded. Projections decide whether that is fatal for them.
	runtimeErr error

	paths    runtime.Paths
	revision string

	status    gateway.Status
	statusErr error

	desiredDigest        string
	appliedDigest        string
	desiredProfileDigest string
	appliedProfileDigest string
	profileDigestErr     error

	recovery RecoveryState
}

func (g gatewayState) drift() bool {
	return g.desiredDigest != g.appliedDigest || g.desiredProfileDigest != g.appliedProfileDigest
}

func (s *Server) gatewayState(ctx context.Context) gatewayState {
	state := gatewayState{revision: fileDigest(s.configPath)}

	cfg, desiredErr := config.Load(s.configPath)
	if desiredErr != nil {
		state.configErr = desiredErr
		cfg, state.runtimeErr = config.LoadRuntime(s.configPath)
	}
	state.cfg = cfg
	state.paths = runtime.NewPaths(cfg)
	state.status, state.statusErr = gateway.New(cfg).Status(ctx)

	// The desired device-policy digest comes from the bundle config.Load
	// prepared, which compiles the document for the configured topology.
	// Reading the document directly here would validate it as if IP-only
	// devices were always allowed and report a digest for a document the
	// current topology cannot apply.
	if cfg.DevicePolicy.Bundle != nil {
		state.desiredDigest = cfg.DevicePolicy.Bundle.Digest
	}
	state.desiredProfileDigest, state.profileDigestErr = config.MihomoProfileDigest(cfg)

	if applied, exists, _ := runtime.LoadState(state.paths.StateFile); exists {
		state.appliedDigest = applied.DevicePolicyDigest
		state.appliedProfileDigest = applied.ProfileDigest
	}

	state.recovery, _ = s.store.Recovery()
	return state
}

func (g gatewayState) stateEvent() StateEvent {
	return StateEvent{
		SchemaVersion:        SchemaVersion,
		Revision:             g.revision,
		Gateway:              g.status.Gateway,
		DesiredDigest:        g.desiredDigest,
		AppliedDigest:        g.appliedDigest,
		DesiredProfileDigest: g.desiredProfileDigest,
		AppliedProfileDigest: g.appliedProfileDigest,
		Drift:                g.drift(),
		Recovery:             g.recovery,
	}
}
