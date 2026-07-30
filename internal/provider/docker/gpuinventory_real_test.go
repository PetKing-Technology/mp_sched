package docker

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

// This canary is opt-in because it reads the host's real NVIDIA inventory.
// It reproduces the 003493 -> 003582 condition without mutating scheduler state.
func TestRealGPUCanary_FreeMemoryOnlyIgnoresLegacyNominalOccupancy(t *testing.T) {
	if os.Getenv("MP_SCHED_REAL_GPU_CANARY") != "1" {
		t.Skip("set MP_SCHED_REAL_GPU_CANARY=1 to inspect the real host GPU inventory")
	}
	ids := strings.Split(os.Getenv("MP_SCHED_REAL_GPU_IDS"), ",")
	if len(ids) == 1 && strings.TrimSpace(ids[0]) == "" {
		ids = []string{"1", "2"}
	}
	inventory, err := QueryNVIDIAGPUs(context.Background(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	policy := config.DockerGPUAdmission{
		MinStartFreeMemoryMB: 20 * 1024,
		ReserveMemoryMB:      10 * 1024,
		LaunchGuardSeconds:   120,
	}
	legacy := []model.Task{{
		TaskID: "003493-legacy-md",
		ResGPU: "true",
		Status: model.TaskStatusRunning,
		Extra:  []byte(`{}`),
	}}
	selected, err := AssignOpportunisticNVIDIADeviceID(
		config.DockerHostResources{GPUIDs: ids},
		policy,
		legacy,
		&model.Task{TaskID: "003582-boltz2", ResGPU: "true"},
		inventory,
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, gpu := range inventory {
		if gpu.Index == selected || strings.EqualFold(gpu.UUID, selected) {
			if gpu.FreeMemoryMB < int64(policy.MinStartFreeMemoryMB) {
				t.Fatalf("selected GPU %s has only %d MiB free", selected, gpu.FreeMemoryMB)
			}
			t.Logf("selected GPU %s with %d MiB real free memory", selected, gpu.FreeMemoryMB)
			return
		}
	}
	t.Fatalf("selected GPU %q was not present in real inventory", selected)
}
