package controlapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"open-mihomo-gateway/internal/macosnetwork"
)

// The same-LAN DHCP takeover recovery flow used to live as an open-coded stage
// check inside every handler that touched it, which let the rules drift apart.
// This file is the single place those rules are written down: which stages an
// intent may run from, which operator confirmations it requires, and which
// stage it lands on. Handlers keep their network effects and feed the outcome
// back in through the intent.
//
// docs/agent-wiki/wiki/concepts/gui-control-plane.md stays authoritative for
// why each rule exists.

type recoveryIntentKind string

const (
	intentPrepare              recoveryIntentKind = "prepare"
	intentSetNotes             recoveryIntentKind = "set_notes"
	intentDiscard              recoveryIntentKind = "discard"
	intentApplyStatic          recoveryIntentKind = "apply_static"
	intentProbeRouterDHCP      recoveryIntentKind = "probe_router_dhcp"
	intentAbandonTakeover      recoveryIntentKind = "abandon_takeover"
	intentClientValidated      recoveryIntentKind = "client_validated"
	intentClientValidationSkip recoveryIntentKind = "client_validation_skip"
	intentRouterRestored       recoveryIntentKind = "router_restored"
	intentManualFinish         recoveryIntentKind = "manual_finish"
	intentKeepStatic           recoveryIntentKind = "keep_static"
	intentRestoreDHCP          recoveryIntentKind = "restore_dhcp"

	// Outcomes of an asynchronous gateway operation.
	intentGatewayStarted recoveryIntentKind = "gateway_started"
	intentGatewayStopped recoveryIntentKind = "gateway_stopped"
	intentStartFailed    recoveryIntentKind = "start_failed"
	intentReloadFailed   recoveryIntentKind = "reload_failed"
)

// recoveryIntent names one recovery action plus everything the rules need to
// decide its outcome: the operator confirmations from the request body, and the
// result of whatever network effect the handler already ran.
type recoveryIntent struct {
	Kind recoveryIntentKind

	// Topology and Snapshot seed a freshly prepared recovery.
	Topology string
	Snapshot *macosnetwork.Snapshot

	// DHCPServers is the outcome of a DHCP OFFER probe. Several transitions
	// branch on whether anything answered.
	DHCPServers []string

	// Operator confirmations carried by the request body.
	GatewayDNSConfirmed         bool
	NoExplicitProxyConfirmed    bool
	IPv6BypassWarningConfirmed  bool
	SkipConfirmed               bool
	RouterDHCPRestoredConfirmed bool
	KeepStaticConfirmed         bool

	// ClientIPv4 is the client whose acceptance evidence was verified.
	ClientIPv4 string

	// Notes replaces the persisted operator notes verbatim.
	Notes string

	// FailureDetail is appended to the operator notes when an asynchronous
	// gateway operation fails.
	FailureDetail string
}

// recoveryRuleError carries the exact status, code and message a rejected
// intent must produce, so every caller reports a precondition the same way.
type recoveryRuleError struct {
	Status  int
	Code    string
	Message string
}

func (e *recoveryRuleError) Error() string { return e.Message }

func recoveryConflict(code, message string) error {
	return &recoveryRuleError{Status: http.StatusConflict, Code: code, Message: message}
}

func recoveryUnprocessable(code, message string) error {
	return &recoveryRuleError{Status: http.StatusUnprocessableEntity, Code: code, Message: message}
}

// recoveryRule is the stage precondition for one intent.
type recoveryRule struct {
	// from lists the stages the intent may run from. An empty list means the
	// intent has no stage precondition.
	from []string
	// requireSnapshot folds the persisted network snapshot into the same
	// precondition as the stage, matching the fused check it replaces.
	requireSnapshot bool
	// missingSnapshotCode, when set, checks the snapshot separately and after
	// the stage, reporting this code instead of recovery_precondition.
	missingSnapshotCode string
	// confirmFirst puts the operator confirmation check before the stage check.
	confirmFirst bool
	message      string
}

var recoveryRules = map[recoveryIntentKind]recoveryRule{
	// Preparing re-runs network discovery and overwrites the persisted
	// snapshot, so it is only safe while the Mac, the router and DHCP are
	// still untouched. From mac_static onward discovery would return the
	// static configuration the operator just applied and destroy the record
	// of what to restore to.
	intentPrepare: {
		from:    []string{RecoveryIdle, RecoveryPrepared, RecoveryComplete, RecoveryCompleteStatic},
		message: "network recovery data can only be prepared before the Mac leaves automatic DHCP",
	},
	intentSetNotes: {},
	intentDiscard: {
		from:    []string{RecoveryPrepared},
		message: "only prepared recovery data can be discarded before network changes begin",
	},
	intentApplyStatic: {
		from:            []string{RecoveryPrepared},
		requireSnapshot: true,
		message:         "prepare a recovery snapshot before setting static IPv4",
	},
	intentProbeRouterDHCP: {
		from:    []string{RecoveryMacStatic},
		message: "Mac static IPv4 must be applied before probing for router DHCP",
	},
	intentAbandonTakeover: {
		from:                []string{RecoveryMacStatic, RecoveryRouterDHCPDisabledConfirmed},
		missingSnapshotCode: "recovery_snapshot_missing",
		message:             "takeover can be abandoned only after the Mac uses fixed IPv4 and before the gateway becomes active",
	},
	intentClientValidated: {
		from:    []string{RecoveryGatewayActive},
		message: "gateway must be active before client acceptance",
	},
	intentClientValidationSkip: {
		from:         []string{RecoveryGatewayActive},
		confirmFirst: true,
		message:      "gateway must be active before skipping client acceptance",
	},
	intentRouterRestored: {
		from:            []string{RecoveryGatewayStopped},
		requireSnapshot: true,
		message:         "stop OpenSurge before verifying restored router DHCP",
	},
	intentManualFinish: {
		from:            []string{RecoveryGatewayStopped},
		requireSnapshot: true,
		confirmFirst:    true,
		message:         "stop OpenSurge before manually finishing network recovery",
	},
	intentKeepStatic: {
		from:            []string{RecoveryGatewayStopped, RecoveryRouterDHCPRestored},
		requireSnapshot: true,
		confirmFirst:    true,
		message:         "stop OpenSurge before finishing the flow with a static Mac IPv4",
	},
	intentRestoreDHCP: {
		from:            []string{RecoveryRouterDHCPRestored},
		requireSnapshot: true,
		message:         "verify restored router DHCP before restoring the Mac",
	},
	intentGatewayStarted: {},
	intentGatewayStopped: {},
	intentStartFailed: {
		from:    []string{RecoveryRouterDHCPDisabledConfirmed},
		message: "a rolled-back start is only recorded while router DHCP is confirmed disabled",
	},
	intentReloadFailed: {},
}

// checkRecovery reports whether an intent may run against the current state.
// Handlers that perform a network effect call it before the effect;
// advanceRecovery repeats it so a direct caller cannot skip it.
func checkRecovery(state RecoveryState, intent recoveryIntent) error {
	rule, ok := recoveryRules[intent.Kind]
	if !ok {
		return fmt.Errorf("unknown recovery intent %q", intent.Kind)
	}
	if rule.confirmFirst {
		if err := checkRecoveryConfirmations(state, intent); err != nil {
			return err
		}
	}
	if err := rule.checkStage(state); err != nil {
		return err
	}
	if !rule.confirmFirst {
		if err := checkRecoveryConfirmations(state, intent); err != nil {
			return err
		}
	}
	return nil
}

// recoveryGateKind names an action outside the recovery flow that is only safe
// at certain stages of a takeover. Gates never move the stage; they read it.
type recoveryGateKind string

const (
	gateStartGateway  recoveryGateKind = "start_gateway"
	gateReloadGateway recoveryGateKind = "reload_gateway"
	gateRestartMihomo recoveryGateKind = "restart_mihomo"
	gateApplySource   recoveryGateKind = "apply_source"
	gateEditConfig    recoveryGateKind = "edit_config"
	gateReadCard      recoveryGateKind = "read_card"
)

// activeTakeoverStages are the stages in which the gateway is actually serving
// the downstream LAN, whether or not client acceptance was collected.
var activeTakeoverStages = []string{RecoveryGatewayActive, RecoveryClientValidated, RecoveryClientValidationSkipped}

type recoveryGate struct {
	// from whitelists stages; except blacklists them. Exactly one is set.
	from   []string
	except []string
	// allowWhenNotRequired passes any stage that no longer needs operator
	// action, in addition to the stages in from.
	allowWhenNotRequired bool
	requireSnapshot      bool
	status               int
	code                 string
	message              string
}

var recoveryGates = map[recoveryGateKind]recoveryGate{
	gateStartGateway: {
		from:    []string{RecoveryRouterDHCPDisabledConfirmed},
		message: "same-LAN DHCP takeover requires persisted confirmation that router DHCP is disabled",
	},
	gateReloadGateway: {
		from:    activeTakeoverStages,
		message: "same-LAN DHCP takeover can reload only while the gateway is active",
	},
	gateRestartMihomo: {
		from:    activeTakeoverStages,
		message: "same-LAN DHCP takeover can restart mihomo only while the gateway is active",
	},
	gateApplySource: {
		from:    activeTakeoverStages,
		message: "same-LAN DHCP takeover can apply a profile only while the gateway is active",
	},
	gateEditConfig: {
		from:                 []string{RecoveryPrepared},
		allowWhenNotRequired: true,
		code:                 "recovery_required",
		message:              "finish network recovery before editing topology",
	},
	gateReadCard: {
		except:          []string{RecoveryIdle},
		requireSnapshot: true,
		status:          http.StatusNotFound,
		code:            "recovery_card_missing",
		message:         "no recovery card is available",
	},
}

// recoveryNeedsCardDiscard reports whether clearing recovery must also destroy a
// prepared offline card rather than simply resetting the state to idle.
func recoveryNeedsCardDiscard(state RecoveryState) bool {
	return state.Stage == RecoveryPrepared
}

// checkRecoveryGate reports whether an action outside the flow may run at the
// current stage. Topology and runtime facts stay with the caller.
func checkRecoveryGate(state RecoveryState, kind recoveryGateKind) error {
	gate, ok := recoveryGates[kind]
	if !ok {
		return fmt.Errorf("unknown recovery gate %q", kind)
	}
	if gate.allowWhenNotRequired && !state.Required {
		return nil
	}
	allowed := len(gate.except) > 0
	for _, stage := range gate.from {
		if state.Stage == stage {
			allowed = true
		}
	}
	for _, stage := range gate.except {
		if state.Stage == stage {
			allowed = false
		}
	}
	if allowed && gate.requireSnapshot && state.NetworkSnapshot == nil {
		allowed = false
	}
	if allowed {
		return nil
	}
	status, code := gate.status, gate.code
	if status == 0 {
		status = http.StatusConflict
	}
	if code == "" {
		code = "recovery_precondition"
	}
	return &recoveryRuleError{Status: status, Code: code, Message: gate.message}
}

// checkRecoveryStage reports only the stage precondition. Handlers that must
// reject a wrong stage before they read the request body use it; the ordering
// between stage and confirmation is itself part of the rules.
func checkRecoveryStage(state RecoveryState, kind recoveryIntentKind) error {
	rule, ok := recoveryRules[kind]
	if !ok {
		return fmt.Errorf("unknown recovery intent %q", kind)
	}
	return rule.checkStage(state)
}

func (r recoveryRule) checkStage(state RecoveryState) error {
	if len(r.from) == 0 {
		return nil
	}
	allowed := false
	for _, stage := range r.from {
		if state.Stage == stage {
			allowed = true
			break
		}
	}
	if !allowed || (r.requireSnapshot && state.NetworkSnapshot == nil) {
		return recoveryConflict("recovery_precondition", r.message)
	}
	if r.missingSnapshotCode != "" && state.NetworkSnapshot == nil {
		return recoveryConflict(r.missingSnapshotCode, "saved network recovery data is missing")
	}
	return nil
}

func checkRecoveryConfirmations(state RecoveryState, intent recoveryIntent) error {
	switch intent.Kind {
	case intentClientValidated:
		if !intent.GatewayDNSConfirmed || !intent.NoExplicitProxyConfirmed {
			return recoveryUnprocessable("client_confirmation_required", "confirm client gateway/DNS and no explicit proxy")
		}
		if state.NetworkSnapshot != nil && state.NetworkSnapshot.IPv6Default && !intent.IPv6BypassWarningConfirmed {
			return recoveryUnprocessable("ipv6_warning_unacknowledged", "acknowledge that IPv6 may bypass cooperative IPv4 policy")
		}
	case intentClientValidationSkip:
		if !intent.SkipConfirmed {
			return recoveryUnprocessable("skip_confirmation_required", "confirm that client DHCP, DNS and TUN evidence will not be validated")
		}
	case intentManualFinish:
		if !intent.RouterDHCPRestoredConfirmed {
			return recoveryUnprocessable("manual_confirmation_required", "confirm that router DHCP has been restored before using the manual recovery fallback")
		}
	case intentKeepStatic:
		if !intent.KeepStaticConfirmed {
			return recoveryUnprocessable("keep_static_confirmation_required", "confirm that the Mac will keep its static IPv4 configuration")
		}
	}
	return nil
}

// advanceRecovery applies an intent to the current state and returns the state
// that should be persisted. It never touches the store or the network.
func advanceRecovery(state RecoveryState, intent recoveryIntent) (RecoveryState, error) {
	if err := checkRecovery(state, intent); err != nil {
		return RecoveryState{}, err
	}
	switch intent.Kind {
	case intentPrepare:
		if intent.Snapshot == nil {
			return RecoveryState{}, fmt.Errorf("prepare requires a network snapshot")
		}
		snapshot := intent.Snapshot
		return RecoveryState{
			SchemaVersion:   SchemaVersion,
			Stage:           RecoveryPrepared,
			Topology:        intent.Topology,
			NetworkService:  snapshot.NetworkService,
			OriginalIPv4:    snapshot.IPv4,
			OriginalRouter:  snapshot.Router,
			Required:        true,
			NetworkSnapshot: snapshot,
		}, nil

	case intentSetNotes:
		state.RecoveryNotes = intent.Notes

	case intentDiscard:
		// The discard path resets through Store.DiscardPreparedRecovery so the
		// offline card is removed with the state; the rule above is the gate.

	case intentApplyStatic:
		state.Stage, state.Required = RecoveryMacStatic, true

	case intentProbeRouterDHCP:
		if len(intent.DHCPServers) > 0 {
			return RecoveryState{}, recoveryConflict("competing_dhcp", "DHCP server is still answering: "+strings.Join(intent.DHCPServers, ", "))
		}
		state.Stage, state.Required = RecoveryRouterDHCPDisabledConfirmed, true

	case intentAbandonTakeover:
		if len(intent.DHCPServers) > 0 {
			appendRecoveryNote(&state, "DHCP takeover abandoned; a DHCP server answered and the Mac was restored to automatic DHCP")
			state.Stage, state.Required = RecoveryComplete, false
		} else {
			appendRecoveryNote(&state, "DHCP takeover abandoned while no DHCP server answered; the Mac remains on fixed IPv4 and router DHCP availability was not verified")
			state.Stage, state.Required = RecoveryCompleteStatic, false
		}

	case intentClientValidated:
		state.Stage = RecoveryClientValidated
		state.ClientValidationSkipped = false
		appendRecoveryNote(&state, fmt.Sprintf("client %s: DHCP ACK, DNS and TUN source observed; gateway/DNS and no explicit proxy confirmed", intent.ClientIPv4))
		state.Required = true

	case intentClientValidationSkip:
		state.Stage = RecoveryClientValidationSkipped
		state.ClientValidationSkipped = true
		appendRecoveryNote(&state, "client DHCP, DNS and TUN acceptance explicitly skipped by operator; no client-path validation evidence was collected")
		state.Required = true

	case intentRouterRestored:
		if len(intent.DHCPServers) == 0 {
			return RecoveryState{}, recoveryConflict("router_dhcp_missing", "no DHCP server answered after the router was marked restored")
		}
		state.Stage, state.Required = RecoveryRouterDHCPRestored, true

	case intentManualFinish:
		appendRecoveryNote(&state, "router DHCP manually confirmed; OFFER evidence skipped; Mac restored to automatic DHCP")
		state.Stage, state.Required = RecoveryComplete, false

	case intentKeepStatic:
		appendRecoveryNote(&state, "post-stop router DHCP verification and Mac automatic DHCP restore explicitly skipped by operator; Mac kept static IPv4")
		state.Stage, state.Required = RecoveryCompleteStatic, false

	case intentRestoreDHCP:
		state.Stage, state.Required = RecoveryComplete, false

	case intentGatewayStarted:
		state.Topology = intent.Topology
		state.Stage = RecoveryGatewayActive
		state.ClientValidationSkipped = false
		state.Required = true

	case intentGatewayStopped:
		state.Topology = intent.Topology
		state.Stage = RecoveryGatewayStopped
		state.Required = true

	case intentStartFailed:
		state.Required = true
		appendRecoveryNote(&state, "gateway start failed and runtime changes were rolled back; router DHCP may remain disabled; resolve the error and retry, or abandon takeover and recover the LAN: "+intent.FailureDetail)

	case intentReloadFailed:
		state.Topology = intent.Topology
		state.Stage = RecoveryRouterDHCPDisabledConfirmed
		state.Required = true
		appendRecoveryNote(&state, "gateway reload failed after services stopped; router DHCP remains disabled; retry start or recover the LAN")
	}
	return state, nil
}

// applyRecovery advances and persists the state without an HTTP response, for
// the asynchronous operation paths.
func (s *Server) applyRecovery(state RecoveryState, intent recoveryIntent) error {
	next, err := advanceRecovery(state, intent)
	if err != nil {
		return err
	}
	return s.store.SaveRecovery(next)
}

// gateRecoveryIntent reports the precondition failure and returns false when an
// intent may not run. Handlers call it before performing a network effect.
func (s *Server) gateRecoveryIntent(w http.ResponseWriter, state RecoveryState, intent recoveryIntent) bool {
	if err := checkRecovery(state, intent); err != nil {
		writeRecoveryError(w, err)
		return false
	}
	return true
}

// gateRecovery reports the stage rule for an action outside the recovery flow.
func (s *Server) gateRecovery(w http.ResponseWriter, state RecoveryState, kind recoveryGateKind) bool {
	if err := checkRecoveryGate(state, kind); err != nil {
		writeRecoveryError(w, err)
		return false
	}
	return true
}

// gateRecoveryStage checks only the stage precondition, for handlers that must
// reject a wrong stage before reading the request body.
func (s *Server) gateRecoveryStage(w http.ResponseWriter, state RecoveryState, kind recoveryIntentKind) bool {
	if err := checkRecoveryStage(state, kind); err != nil {
		writeRecoveryError(w, err)
		return false
	}
	return true
}

// commitRecovery advances the state, persists it, and reports any failure.
func (s *Server) commitRecovery(w http.ResponseWriter, state RecoveryState, intent recoveryIntent) (RecoveryState, bool) {
	next, err := advanceRecovery(state, intent)
	if err != nil {
		writeRecoveryError(w, err)
		return RecoveryState{}, false
	}
	if err := s.store.SaveRecovery(next); err != nil {
		writeError(w, http.StatusInternalServerError, "recovery_write_failed", err.Error())
		return RecoveryState{}, false
	}
	return next, true
}

func writeRecoveryError(w http.ResponseWriter, err error) {
	var ruleErr *recoveryRuleError
	if errors.As(err, &ruleErr) {
		writeError(w, ruleErr.Status, ruleErr.Code, ruleErr.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, "recovery_failed", err.Error())
}
