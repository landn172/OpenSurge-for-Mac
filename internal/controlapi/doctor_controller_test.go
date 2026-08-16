package controlapi

import (
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/doctor"
)

func TestDoctorControllerIsSingleFlightAndKeepsResultRevision(t *testing.T) {
	release := make(chan struct{})
	controller := newDoctorController(func(config.Config) doctor.Report {
		<-release
		return doctor.Report{Checks: []doctor.Check{{Name: "config", OK: true}}}
	})
	first, started := controller.start(config.Config{}, "revision-a")
	if !started || first.State != doctorRunRunning {
		t.Fatalf("first = %#v started=%v", first, started)
	}
	second, started := controller.start(config.Config{}, "revision-b")
	if started || second.Revision != "revision-a" {
		t.Fatalf("second = %#v started=%v", second, started)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status := controller.snapshot("revision-a")
		if status.State == doctorRunSucceeded {
			if !status.Current || !status.Healthy || len(status.Checks) != 1 {
				t.Fatalf("status = %#v", status)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Doctor did not complete")
}
