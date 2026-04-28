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

func TestCheckDockerGPUOccupancy_NoGpuIdsButTaskWantsGPU(t *testing.T) {
	host := config.DockerHostResources{}
	tasks := []model.Task{{TaskID: "a", ResGPU: "1"}}
	if err := CheckDockerGPUOccupancy(host, tasks); err == nil {
		t.Fatal("expected error")
	}
}
