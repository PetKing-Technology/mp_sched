package docker

import (
	"testing"

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

func TestAssignNVIDIADeviceIDForTaskUsesFirstFreeSlot(t *testing.T) {
	host := config.DockerHostResources{GPUIDs: []string{"GPU-0", "GPU-0", "GPU-1"}}
	first, err := AssignNVIDIADeviceIDForTask(host, nil, &model.Task{TaskID: "first", ResGPU: "true"})
	if err != nil || first != "GPU-0" { t.Fatalf("first=%q err=%v", first, err) }
	extra, err := ExtraWithAssignedNVIDIAGPUID([]byte(`{}`), first)
	if err != nil { t.Fatal(err) }
	second, err := AssignNVIDIADeviceIDForTask(host, []model.Task{{TaskID: "first", ResGPU: "true", Extra: extra}}, &model.Task{TaskID: "second", ResGPU: "true"})
	if err != nil || second != "GPU-0" { t.Fatalf("second=%q err=%v", second, err) }
}

// 同一 id 在 gpu_ids 出现两次 = 两路并发槽；单任务挂载去重后只占 GPU-0 一槽调度记账
func TestTaskGPUSlotNeed_OneSlotPerTaskOnDedupedAttach(t *testing.T) {
	hostCap := map[string]int{"GPU-0": 2}
	hr := &config.DockerHostResources{GPUIDs: []string{"GPU-0", "GPU-0"}}
	m, err := TaskGPUSlotNeed("true", hostCap, hr)
	if err != nil {
		t.Fatal(err)
	}
	if m["GPU-0"] != 1 {
		t.Fatalf("got %+v", m)
	}
}

func TestCheckDockerGPUOccupancy_ThreeTasksExceedTwoSlots(t *testing.T) {
	host := config.DockerHostResources{GPUIDs: []string{"GPU-0", "GPU-0"}}
	tasks := []model.Task{
		{TaskID: "a", ResGPU: "true"},
		{TaskID: "b", ResGPU: "true"},
		{TaskID: "c", ResGPU: "true"},
	}
	if err := CheckDockerGPUOccupancy(host, tasks); err == nil {
		t.Fatal("expected error: 3 tasks need 3 slots, host has 2")
	}
}

func TestCheckDockerGPUOccupancy_TwoSlotsShared(t *testing.T) {
	host := config.DockerHostResources{GPUIDs: []string{"GPU-0", "GPU-0"}}
	tasks := []model.Task{
		{TaskID: "a", ResGPU: "true"},
		{TaskID: "b", ResGPU: "true"},
	}
	if err := CheckDockerGPUOccupancy(host, tasks); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDockerGPUOccupancyWithCandidate_ExceedsWhenIncludingPending(t *testing.T) {
	host := config.DockerHostResources{GPUIDs: []string{"GPU-0"}}
	occ := []model.Task{{TaskID: "a", ResGPU: "true"}}
	cand := &model.Task{TaskID: "b", ResGPU: "true"}
	if err := CheckDockerGPUOccupancyWithCandidate(host, occ, cand); err == nil {
		t.Fatal("expected exhausted: 2 needs 1 slot")
	}
}

func TestCheckDockerGPUOccupancyWithCandidate_Fits(t *testing.T) {
	host := config.DockerHostResources{GPUIDs: []string{"GPU-0", "GPU-0"}}
	occ := []model.Task{{TaskID: "a", ResGPU: "true"}}
	cand := &model.Task{TaskID: "b", ResGPU: "true"}
	if err := CheckDockerGPUOccupancyWithCandidate(host, occ, cand); err != nil {
		t.Fatal(err)
	}
}
