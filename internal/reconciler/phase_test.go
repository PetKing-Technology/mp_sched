package reconciler

import (
	"testing"

	"mp_sched/internal/provider"
)

func TestWorkloadGoneForClear(t *testing.T) {
	if !workloadGoneForClear(provider.PhaseSucceeded) {
		t.Fatalf("succeeded should clear")
	}
	if !workloadGoneForClear(provider.PhaseFailed) {
		t.Fatalf("failed should clear")
	}
	if !workloadGoneForClear(provider.PhaseStopped) {
		t.Fatalf("stopped should clear")
	}
	if workloadGoneForClear(provider.PhaseRunning) {
		t.Fatalf("running should not clear")
	}
	if workloadGoneForClear(provider.PhaseUnknown) {
		t.Fatalf("unknown should not clear")
	}
}
