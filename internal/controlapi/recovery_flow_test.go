package controlapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/macosnetwork"
)

func stateAt(stage string) RecoveryState {
	return RecoveryState{SchemaVersion: SchemaVersion, Stage: stage}
}

func stateAtWithSnapshot(stage string) RecoveryState {
	state := stateAt(stage)
	state.NetworkSnapshot = &macosnetwork.Snapshot{
		NetworkService: "Wi-Fi",
		Interface:      "en0",
		IPv4:           "192.168.1.20",
		SubnetMask:     "255.255.255.0",
		Router:         "192.168.1.1",
	}
	return state
}

func requireRuleError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var ruleErr *recoveryRuleError
	if !errors.As(err, &ruleErr) {
		t.Fatalf("expected a recovery rule error, got %v", err)
	}
	if ruleErr.Status != status || ruleErr.Code != code {
		t.Fatalf("expected %d/%s, got %d/%s (%s)", status, code, ruleErr.Status, ruleErr.Code, ruleErr.Message)
	}
	if ruleErr.Message == "" {
		t.Fatal("rule error must carry an operator-facing message")
	}
}

// Every stage precondition in the flow, as data. This is the table the removed
// allowedRecoveryTransition contradicted; it now matches the handlers.
func TestRecoveryStagePreconditions(t *testing.T) {
	allStages := []string{
		RecoveryIdle, RecoveryPrepared, RecoveryMacStatic, RecoveryRouterDHCPDisabledConfirmed,
		RecoveryGatewayActive, RecoveryClientValidated, RecoveryClientValidationSkipped,
		RecoveryGatewayStopped, RecoveryRouterDHCPRestored, RecoveryComplete, RecoveryCompleteStatic,
	}

	cases := []struct {
		kind    recoveryIntentKind
		allowed []string
	}{
		{intentPrepare, []string{RecoveryIdle, RecoveryPrepared, RecoveryComplete, RecoveryCompleteStatic}},
		{intentDiscard, []string{RecoveryPrepared}},
		{intentApplyStatic, []string{RecoveryPrepared}},
		{intentProbeRouterDHCP, []string{RecoveryMacStatic}},
		{intentAbandonTakeover, []string{RecoveryMacStatic, RecoveryRouterDHCPDisabledConfirmed}},
		{intentClientValidated, []string{RecoveryGatewayActive}},
		{intentClientValidationSkip, []string{RecoveryGatewayActive}},
		{intentRouterRestored, []string{RecoveryGatewayStopped}},
		{intentManualFinish, []string{RecoveryGatewayStopped}},
		{intentKeepStatic, []string{RecoveryGatewayStopped, RecoveryRouterDHCPRestored}},
		{intentRestoreDHCP, []string{RecoveryRouterDHCPRestored}},
	}

	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			allowed := map[string]bool{}
			for _, stage := range tc.allowed {
				allowed[stage] = true
			}
			for _, stage := range allStages {
				err := checkRecoveryStage(stateAtWithSnapshot(stage), tc.kind)
				if allowed[stage] && err != nil {
					t.Errorf("stage %s should be allowed, got %v", stage, err)
				}
				if !allowed[stage] && err == nil {
					t.Errorf("stage %s should be rejected", stage)
				}
			}
		})
	}
}

// Operator notes may be recorded at any stage; the stage itself never moves.
func TestRecoverySetNotesHasNoStagePrecondition(t *testing.T) {
	for _, stage := range []string{RecoveryIdle, RecoveryGatewayActive, RecoveryComplete} {
		if err := checkRecoveryStage(stateAt(stage), intentSetNotes); err != nil {
			t.Errorf("set notes at %s: unexpected precondition %v", stage, err)
		}
	}
}

// Preparing after the Mac left automatic DHCP would overwrite the very record
// the flow exists to restore from.
func TestRecoveryPrepareRefusesToOverwriteAChangedNetwork(t *testing.T) {
	for _, stage := range []string{
		RecoveryMacStatic, RecoveryRouterDHCPDisabledConfirmed, RecoveryGatewayActive,
		RecoveryClientValidated, RecoveryClientValidationSkipped,
		RecoveryGatewayStopped, RecoveryRouterDHCPRestored,
	} {
		requireRuleError(t, checkRecoveryStage(stateAtWithSnapshot(stage), intentPrepare), http.StatusConflict, "recovery_precondition")
	}
}

func TestRecoveryPreconditionsRequireNetworkSnapshot(t *testing.T) {
	fused := []recoveryIntentKind{intentApplyStatic, intentRouterRestored, intentManualFinish, intentKeepStatic, intentRestoreDHCP}
	stages := map[recoveryIntentKind]string{
		intentApplyStatic:    RecoveryPrepared,
		intentRouterRestored: RecoveryGatewayStopped,
		intentManualFinish:   RecoveryGatewayStopped,
		intentKeepStatic:     RecoveryGatewayStopped,
		intentRestoreDHCP:    RecoveryRouterDHCPRestored,
	}
	for _, kind := range fused {
		err := checkRecoveryStage(stateAt(stages[kind]), kind)
		requireRuleError(t, err, http.StatusConflict, "recovery_precondition")
	}

	// abandon-takeover reports the missing snapshot separately, after the stage.
	err := checkRecoveryStage(stateAt(RecoveryMacStatic), intentAbandonTakeover)
	requireRuleError(t, err, http.StatusConflict, "recovery_snapshot_missing")
	err = checkRecoveryStage(stateAt(RecoveryIdle), intentAbandonTakeover)
	requireRuleError(t, err, http.StatusConflict, "recovery_precondition")
}

// The order between the stage check and the operator confirmation check differs
// per intent, and the difference is observable as a different status code.
func TestRecoveryConfirmationOrdering(t *testing.T) {
	cases := []struct {
		name   string
		state  RecoveryState
		intent recoveryIntent
		status int
		code   string
	}{
		{
			name:   "client validation checks stage before confirmations",
			state:  stateAtWithSnapshot(RecoveryComplete),
			intent: recoveryIntent{Kind: intentClientValidated},
			status: http.StatusConflict,
			code:   "recovery_precondition",
		},
		{
			name:   "client validation skip checks confirmations before stage",
			state:  stateAtWithSnapshot(RecoveryComplete),
			intent: recoveryIntent{Kind: intentClientValidationSkip},
			status: http.StatusUnprocessableEntity,
			code:   "skip_confirmation_required",
		},
		{
			name:   "manual finish checks confirmations before stage",
			state:  stateAtWithSnapshot(RecoveryComplete),
			intent: recoveryIntent{Kind: intentManualFinish},
			status: http.StatusUnprocessableEntity,
			code:   "manual_confirmation_required",
		},
		{
			name:   "keep static checks confirmations before stage",
			state:  stateAtWithSnapshot(RecoveryComplete),
			intent: recoveryIntent{Kind: intentKeepStatic},
			status: http.StatusUnprocessableEntity,
			code:   "keep_static_confirmation_required",
		},
		{
			name:   "client validation requires gateway and proxy confirmation",
			state:  stateAtWithSnapshot(RecoveryGatewayActive),
			intent: recoveryIntent{Kind: intentClientValidated, GatewayDNSConfirmed: true},
			status: http.StatusUnprocessableEntity,
			code:   "client_confirmation_required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireRuleError(t, checkRecovery(tc.state, tc.intent), tc.status, tc.code)
		})
	}
}

func TestRecoveryIPv6WarningOnlyWhenSnapshotDefaultsToIPv6(t *testing.T) {
	intent := recoveryIntent{Kind: intentClientValidated, GatewayDNSConfirmed: true, NoExplicitProxyConfirmed: true}

	state := stateAtWithSnapshot(RecoveryGatewayActive)
	if err := checkRecovery(state, intent); err != nil {
		t.Fatalf("no IPv6 default should not require acknowledgement: %v", err)
	}

	state.NetworkSnapshot.IPv6Default = true
	requireRuleError(t, checkRecovery(state, intent), http.StatusUnprocessableEntity, "ipv6_warning_unacknowledged")

	intent.IPv6BypassWarningConfirmed = true
	if err := checkRecovery(state, intent); err != nil {
		t.Fatalf("acknowledged IPv6 warning should pass: %v", err)
	}
}

// Transitions whose target stage depends on the outcome of the network effect
// the handler already ran.
func TestRecoveryTransitionsBranchOnProbeOutcome(t *testing.T) {
	cases := []struct {
		name         string
		state        RecoveryState
		intent       recoveryIntent
		wantStage    string
		wantRequired bool
		wantNote     bool
		wantStatus   int
		wantCode     string
	}{
		{
			name:       "competing DHCP blocks the takeover confirmation",
			state:      stateAtWithSnapshot(RecoveryMacStatic),
			intent:     recoveryIntent{Kind: intentProbeRouterDHCP, DHCPServers: []string{"192.168.1.1"}},
			wantStatus: http.StatusConflict,
			wantCode:   "competing_dhcp",
		},
		{
			name:         "a silent LAN confirms router DHCP is disabled",
			state:        stateAtWithSnapshot(RecoveryMacStatic),
			intent:       recoveryIntent{Kind: intentProbeRouterDHCP},
			wantStage:    RecoveryRouterDHCPDisabledConfirmed,
			wantRequired: true,
		},
		{
			name:         "abandoning with an answering server restores DHCP",
			state:        stateAtWithSnapshot(RecoveryMacStatic),
			intent:       recoveryIntent{Kind: intentAbandonTakeover, DHCPServers: []string{"192.168.1.1"}},
			wantStage:    RecoveryComplete,
			wantRequired: false,
			wantNote:     true,
		},
		{
			name:         "abandoning with no answer ends on static IPv4",
			state:        stateAtWithSnapshot(RecoveryMacStatic),
			intent:       recoveryIntent{Kind: intentAbandonTakeover},
			wantStage:    RecoveryCompleteStatic,
			wantRequired: false,
			wantNote:     true,
		},
		{
			name:       "router restore needs an actual OFFER",
			state:      stateAtWithSnapshot(RecoveryGatewayStopped),
			intent:     recoveryIntent{Kind: intentRouterRestored},
			wantStatus: http.StatusConflict,
			wantCode:   "router_dhcp_missing",
		},
		{
			name:         "an answering router advances to restored",
			state:        stateAtWithSnapshot(RecoveryGatewayStopped),
			intent:       recoveryIntent{Kind: intentRouterRestored, DHCPServers: []string{"192.168.1.1"}},
			wantStage:    RecoveryRouterDHCPRestored,
			wantRequired: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, err := advanceRecovery(tc.state, tc.intent)
			if tc.wantCode != "" {
				requireRuleError(t, err, tc.wantStatus, tc.wantCode)
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if next.Stage != tc.wantStage {
				t.Errorf("stage: want %s, got %s", tc.wantStage, next.Stage)
			}
			if next.Required != tc.wantRequired {
				t.Errorf("required: want %v, got %v", tc.wantRequired, next.Required)
			}
			if tc.wantNote && next.RecoveryNotes == "" {
				t.Error("expected an operator note to be recorded")
			}
		})
	}
}

func TestRecoveryTerminalTransitions(t *testing.T) {
	cases := []struct {
		name         string
		state        RecoveryState
		intent       recoveryIntent
		wantStage    string
		wantRequired bool
	}{
		{
			name:         "apply static",
			state:        stateAtWithSnapshot(RecoveryPrepared),
			intent:       recoveryIntent{Kind: intentApplyStatic},
			wantStage:    RecoveryMacStatic,
			wantRequired: true,
		},
		{
			name:         "client validated",
			state:        stateAtWithSnapshot(RecoveryGatewayActive),
			intent:       recoveryIntent{Kind: intentClientValidated, ClientIPv4: "192.168.1.120", GatewayDNSConfirmed: true, NoExplicitProxyConfirmed: true},
			wantStage:    RecoveryClientValidated,
			wantRequired: true,
		},
		{
			name:         "client validation skipped",
			state:        stateAtWithSnapshot(RecoveryGatewayActive),
			intent:       recoveryIntent{Kind: intentClientValidationSkip, SkipConfirmed: true},
			wantStage:    RecoveryClientValidationSkipped,
			wantRequired: true,
		},
		{
			name:         "manual finish",
			state:        stateAtWithSnapshot(RecoveryGatewayStopped),
			intent:       recoveryIntent{Kind: intentManualFinish, RouterDHCPRestoredConfirmed: true},
			wantStage:    RecoveryComplete,
			wantRequired: false,
		},
		{
			name:         "keep static from gateway stopped",
			state:        stateAtWithSnapshot(RecoveryGatewayStopped),
			intent:       recoveryIntent{Kind: intentKeepStatic, KeepStaticConfirmed: true},
			wantStage:    RecoveryCompleteStatic,
			wantRequired: false,
		},
		{
			name:         "keep static from router restored",
			state:        stateAtWithSnapshot(RecoveryRouterDHCPRestored),
			intent:       recoveryIntent{Kind: intentKeepStatic, KeepStaticConfirmed: true},
			wantStage:    RecoveryCompleteStatic,
			wantRequired: false,
		},
		{
			name:         "restore DHCP",
			state:        stateAtWithSnapshot(RecoveryRouterDHCPRestored),
			intent:       recoveryIntent{Kind: intentRestoreDHCP},
			wantStage:    RecoveryComplete,
			wantRequired: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, err := advanceRecovery(tc.state, tc.intent)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if next.Stage != tc.wantStage {
				t.Errorf("stage: want %s, got %s", tc.wantStage, next.Stage)
			}
			if next.Required != tc.wantRequired {
				t.Errorf("required: want %v, got %v", tc.wantRequired, next.Required)
			}
		})
	}
}

// Every transition appends to the operator notes; none of them may drop what
// an earlier stage recorded.
func TestRecoveryClientValidationAndSkipBothAppendNotes(t *testing.T) {
	state := stateAtWithSnapshot(RecoveryGatewayActive)
	state.RecoveryNotes = "earlier note"

	validated, err := advanceRecovery(state, recoveryIntent{
		Kind: intentClientValidated, ClientIPv4: "192.168.1.120",
		GatewayDNSConfirmed: true, NoExplicitProxyConfirmed: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if validated.ClientValidationSkipped {
		t.Fatalf("unexpected validated state: %+v", validated)
	}
	if !strings.Contains(validated.RecoveryNotes, "earlier note") {
		t.Errorf("client acceptance dropped an earlier note, got %q", validated.RecoveryNotes)
	}
	if !strings.Contains(validated.RecoveryNotes, "192.168.1.120") {
		t.Errorf("client acceptance must record the validated client, got %q", validated.RecoveryNotes)
	}

	skipped, err := advanceRecovery(state, recoveryIntent{Kind: intentClientValidationSkip, SkipConfirmed: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !skipped.ClientValidationSkipped {
		t.Error("skip must record that no client evidence was collected")
	}
	if !strings.Contains(skipped.RecoveryNotes, "earlier note") {
		t.Errorf("skip appends to notes, got %q", skipped.RecoveryNotes)
	}
}

func TestRecoveryPrepareBuildsStateFromSnapshot(t *testing.T) {
	snapshot := macosnetwork.Snapshot{
		NetworkService: "Wi-Fi", Interface: "en0",
		IPv4: "192.168.1.20", SubnetMask: "255.255.255.0", Router: "192.168.1.1",
	}
	next, err := advanceRecovery(stateAt(RecoveryIdle), recoveryIntent{
		Kind: intentPrepare, Topology: "same_wifi_dhcp", Snapshot: &snapshot,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next.Stage != RecoveryPrepared || !next.Required {
		t.Fatalf("unexpected prepared state: %+v", next)
	}
	if next.Topology != "same_wifi_dhcp" || next.NetworkService != "Wi-Fi" ||
		next.OriginalIPv4 != "192.168.1.20" || next.OriginalRouter != "192.168.1.1" {
		t.Fatalf("prepared state must mirror the snapshot: %+v", next)
	}
	if next.NetworkSnapshot == nil {
		t.Fatal("prepared state must persist the snapshot")
	}
}

// Notes may be replaced at any stage, but never the stage itself.
func TestRecoverySetNotesNeverMovesTheStage(t *testing.T) {
	for _, stage := range []string{RecoveryIdle, RecoveryGatewayActive, RecoveryGatewayStopped} {
		state := stateAt(stage)
		next, err := advanceRecovery(state, recoveryIntent{Kind: intentSetNotes, Notes: "operator note"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next.Stage != stage {
			t.Errorf("stage moved from %s to %s", stage, next.Stage)
		}
		if next.RecoveryNotes != "operator note" {
			t.Errorf("notes: got %q", next.RecoveryNotes)
		}
	}
}

// Outcomes of an asynchronous gateway operation.
func TestRecoveryOperationOutcomes(t *testing.T) {
	t.Run("start clears a previous validation waiver", func(t *testing.T) {
		state := stateAtWithSnapshot(RecoveryRouterDHCPDisabledConfirmed)
		state.ClientValidationSkipped = true
		next, err := advanceRecovery(state, recoveryIntent{Kind: intentGatewayStarted, Topology: "same_wifi_dhcp"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next.Stage != RecoveryGatewayActive || !next.Required || next.ClientValidationSkipped {
			t.Fatalf("unexpected started state: %+v", next)
		}
		if next.Topology != "same_wifi_dhcp" {
			t.Errorf("topology: got %q", next.Topology)
		}
	})

	t.Run("stop keeps a recorded validation waiver", func(t *testing.T) {
		state := stateAtWithSnapshot(RecoveryClientValidationSkipped)
		state.ClientValidationSkipped = true
		next, err := advanceRecovery(state, recoveryIntent{Kind: intentGatewayStopped, Topology: "same_wifi_dhcp"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next.Stage != RecoveryGatewayStopped || !next.Required || !next.ClientValidationSkipped {
			t.Fatalf("unexpected stopped state: %+v", next)
		}
	})

	t.Run("a rolled-back start keeps its stage and warns", func(t *testing.T) {
		state := stateAtWithSnapshot(RecoveryRouterDHCPDisabledConfirmed)
		next, err := advanceRecovery(state, recoveryIntent{Kind: intentStartFailed, FailureDetail: "dnsmasq refused to start"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next.Stage != RecoveryRouterDHCPDisabledConfirmed || !next.Required {
			t.Fatalf("unexpected failed-start state: %+v", next)
		}
		if !strings.Contains(next.RecoveryNotes, "dnsmasq refused to start") {
			t.Errorf("failure detail must reach the operator note, got %q", next.RecoveryNotes)
		}
	})

	t.Run("a rolled-back start is only recorded from the confirmed stage", func(t *testing.T) {
		for _, stage := range []string{RecoveryIdle, RecoveryGatewayActive, RecoveryComplete} {
			_, err := advanceRecovery(stateAtWithSnapshot(stage), recoveryIntent{Kind: intentStartFailed})
			requireRuleError(t, err, http.StatusConflict, "recovery_precondition")
		}
	})

	t.Run("a failed reload returns to the restartable stage", func(t *testing.T) {
		next, err := advanceRecovery(stateAtWithSnapshot(RecoveryGatewayActive), recoveryIntent{Kind: intentReloadFailed, Topology: "same_wifi_dhcp"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next.Stage != RecoveryRouterDHCPDisabledConfirmed || !next.Required {
			t.Fatalf("unexpected failed-reload state: %+v", next)
		}
		if next.RecoveryNotes == "" {
			t.Error("expected an operator note")
		}
	})
}

// Actions outside the flow that a takeover stage may block.
func TestRecoveryGates(t *testing.T) {
	allStages := []string{
		RecoveryIdle, RecoveryPrepared, RecoveryMacStatic, RecoveryRouterDHCPDisabledConfirmed,
		RecoveryGatewayActive, RecoveryClientValidated, RecoveryClientValidationSkipped,
		RecoveryGatewayStopped, RecoveryRouterDHCPRestored, RecoveryComplete, RecoveryCompleteStatic,
	}

	cases := []struct {
		kind    recoveryGateKind
		allowed []string
	}{
		{gateStartGateway, []string{RecoveryRouterDHCPDisabledConfirmed}},
		{gateReloadGateway, activeTakeoverStages},
		{gateRestartMihomo, activeTakeoverStages},
		{gateApplySource, activeTakeoverStages},
	}

	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			allowed := map[string]bool{}
			for _, stage := range tc.allowed {
				allowed[stage] = true
			}
			for _, stage := range allStages {
				state := stateAtWithSnapshot(stage)
				state.Required = true
				err := checkRecoveryGate(state, tc.kind)
				if allowed[stage] && err != nil {
					t.Errorf("stage %s should be allowed, got %v", stage, err)
				}
				if !allowed[stage] {
					requireRuleError(t, err, http.StatusConflict, "recovery_precondition")
				}
			}
		})
	}
}

// Editing the configuration is blocked only while recovery still needs operator
// action, and prepared recovery data may always be corrected.
func TestRecoveryEditConfigGate(t *testing.T) {
	for _, stage := range []string{RecoveryIdle, RecoveryComplete, RecoveryCompleteStatic} {
		state := stateAt(stage)
		if err := checkRecoveryGate(state, gateEditConfig); err != nil {
			t.Errorf("stage %s with no pending recovery should be editable, got %v", stage, err)
		}
	}

	prepared := stateAtWithSnapshot(RecoveryPrepared)
	prepared.Required = true
	if err := checkRecoveryGate(prepared, gateEditConfig); err != nil {
		t.Errorf("prepared recovery must stay correctable, got %v", err)
	}

	for _, stage := range []string{RecoveryMacStatic, RecoveryGatewayActive, RecoveryGatewayStopped} {
		state := stateAtWithSnapshot(stage)
		state.Required = true
		requireRuleError(t, checkRecoveryGate(state, gateEditConfig), http.StatusConflict, "recovery_required")
	}
}

// The offline recovery card exists from the moment recovery leaves idle.
func TestRecoveryCardGate(t *testing.T) {
	requireRuleError(t, checkRecoveryGate(stateAt(RecoveryIdle), gateReadCard), http.StatusNotFound, "recovery_card_missing")
	requireRuleError(t, checkRecoveryGate(stateAt(RecoveryPrepared), gateReadCard), http.StatusNotFound, "recovery_card_missing")

	for _, stage := range []string{RecoveryPrepared, RecoveryMacStatic, RecoveryGatewayActive, RecoveryComplete, RecoveryCompleteStatic} {
		if err := checkRecoveryGate(stateAtWithSnapshot(stage), gateReadCard); err != nil {
			t.Errorf("stage %s should expose the card, got %v", stage, err)
		}
	}

	idleWithSnapshot := stateAtWithSnapshot(RecoveryIdle)
	requireRuleError(t, checkRecoveryGate(idleWithSnapshot, gateReadCard), http.StatusNotFound, "recovery_card_missing")
}

func TestRecoveryCardDiscardOnlyFromPrepared(t *testing.T) {
	if !recoveryNeedsCardDiscard(stateAt(RecoveryPrepared)) {
		t.Error("a prepared card must be destroyed, not merely reset")
	}
	for _, stage := range []string{RecoveryIdle, RecoveryComplete, RecoveryCompleteStatic} {
		if recoveryNeedsCardDiscard(stateAt(stage)) {
			t.Errorf("stage %s has no prepared card to discard", stage)
		}
	}
}

func TestUnknownRecoveryIntentIsRejected(t *testing.T) {
	if _, err := advanceRecovery(stateAt(RecoveryIdle), recoveryIntent{Kind: "not_a_real_intent"}); err == nil {
		t.Fatal("expected an unknown intent to be rejected")
	}
	if err := checkRecoveryGate(stateAt(RecoveryIdle), "not_a_real_gate"); err == nil {
		t.Fatal("expected an unknown gate to be rejected")
	}
}
