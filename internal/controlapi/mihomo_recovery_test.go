package controlapi

import "testing"

func TestMihomoRecoveryRetriesProcessLossOnlyOncePerIncident(t *testing.T) {
	controller := newMihomoRecoveryController()
	if !controller.observeFailure(mihomoFailureProcessMissing) {
		t.Fatal("missing process should immediately qualify for one restart")
	}
	if !controller.begin(mihomoFailureProcessMissing) {
		t.Fatal("qualified incident should begin")
	}
	controller.finishAutomatic(nil)
	controller.observeHealthy()
	if state := controller.snapshot().State; state != mihomoRecoveryRecovering {
		t.Fatalf("after one healthy sample state = %q", state)
	}
	controller.observeHealthy()
	if state := controller.snapshot().State; state != mihomoRecoveryIdle {
		t.Fatalf("after two healthy samples state = %q", state)
	}
}

func TestMihomoRecoveryRequiresTwoRefusals(t *testing.T) {
	controller := newMihomoRecoveryController()
	if controller.observeFailure(mihomoFailureControllerRefused) {
		t.Fatal("one refused controller sample must not restart")
	}
	if !controller.observeFailure(mihomoFailureControllerRefused) {
		t.Fatal("two refused controller samples should restart")
	}
}
