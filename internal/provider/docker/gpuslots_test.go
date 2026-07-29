package docker

import (
	"testing"

	"gorm.io/datatypes"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

func TestTaskWantsGPU(t *testing.T) {
	if TaskWantsGPU("") || TaskWantsGPU("0") || TaskWantsGPU("false") || TaskWantsGPU("OFF") {
		t.Fatal("expected off")
	}
	if !TaskWantsGPU("1") || !TaskWantsGPU("true") || !TaskWantsGPU("yes") || !TaskWantsGPU("ON") {
		t.Fatal("expected on")
	}
	if TaskWantsGPU("GPU-0") || TaskWantsGPU("all") {
		t.Fatal("legacy strings must be off; use true/1/yes/on only")
	}
}

// 同一 id 在 gpu_ids 出现两次 = 两路并发槽；每个任务只分配其中一个槽位。
func TestAssignNVIDIADeviceIDForTask_FirstAvailableSlot(t *testing.T) {
	host := config.DockerHostResources{GPUIDs: []string{"GPU-0", "GPU-0", "GPU-1"}}
	id, err := AssignNVIDIADeviceIDForTask(host, nil, &model.Task{TaskID: "a", ResGPU: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "GPU-0" {
		t.Fatalf("got %q", id)
	}
}

func TestCheckDockerGPUOccupancy_ThreeTasksExceedTwoSlots(t *testing.T) {
	host := config.DockerHostResources{GPUIDs: []string{"GPU-0", "GPU-0"}}
	tasks := []model.Task{
		gpuTask(t, "a", "GPU-0"),
		gpuTask(t, "b", "GPU-0"),
		gpuTask(t, "c", "GPU-0"),
	}
	if err := CheckDockerGPUOccupancy(host, tasks); err == nil {
		t.Fatal("expected error: 3 tasks need 3 slots, host has 2")
	}
}

func TestCheckDockerGPUOccupancy_TwoSlotsShared(t *testing.T) {
	host := config.DockerHostResources{GPUIDs: []string{"GPU-0", "GPU-0"}}
	tasks := []model.Task{
		gpuTask(t, "a", "GPU-0"),
		gpuTask(t, "b", "GPU-0"),
	}
	if err := CheckDockerGPUOccupancy(host, tasks); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDockerGPUOccupancyWithCandidate_ExceedsWhenIncludingPending(t *testing.T) {
	host := config.DockerHostResources{GPUIDs: []string{"GPU-0"}}
	occ := []model.Task{gpuTask(t, "a", "GPU-0")}
	cand := &model.Task{TaskID: "b", ResGPU: "true"}
	if err := CheckDockerGPUOccupancyWithCandidate(host, occ, cand); err == nil {
		t.Fatal("expected exhausted: 2 needs 1 slot")
	}
}

func TestCheckDockerGPUOccupancyWithCandidate_Fits(t *testing.T) {
	host := config.DockerHostResources{GPUIDs: []string{"GPU-0", "GPU-0"}}
	occ := []model.Task{gpuTask(t, "a", "GPU-0")}
	cand := &model.Task{TaskID: "b", ResGPU: "true"}
	if err := CheckDockerGPUOccupancyWithCandidate(host, occ, cand); err != nil {
		t.Fatal(err)
	}
}

func TestAvailableNVIDIADeviceIDs_RemovesOnlyOneDuplicateSlot(t *testing.T) {
	host := config.DockerHostResources{GPUIDs: []string{"GPU-0", "GPU-0", "GPU-1"}}
	occ := []model.Task{gpuTask(t, "a", "GPU-0")}
	ids, err := AvailableNVIDIADeviceIDs(host, occ)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "GPU-0" || ids[1] != "GPU-1" {
		t.Fatalf("available: %v", ids)
	}
}

func gpuTask(t *testing.T, taskID string, gpuID string) model.Task {
	t.Helper()
	extra, err := ExtraWithAssignedNVIDIAGPUID([]byte(`{}`), gpuID)
	if err != nil {
		t.Fatal(err)
	}
	return model.Task{TaskID: taskID, ResGPU: "true", Extra: datatypes.JSON(extra)}
}
