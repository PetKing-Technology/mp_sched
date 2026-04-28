package docker

import (
	"testing"

	"mp_sched/internal/model"
)

func TestHostGPUSlotCapacity_Dupes(t *testing.T) {
	cap := HostGPUSlotCapacity([]string{"GPU-0", "GPU-0", "GPU-1"})
	if cap["GPU-0"] != 2 || cap["GPU-1"] != 1 {
		t.Fatalf("%v", cap)
	}
}

func TestTaskGPUSlotNeed_DupesInRequest(t *testing.T) {
	hostCap := map[string]int{"GPU-0": 2}
	m, err := TaskGPUSlotNeed("GPU-0,GPU-0", hostCap)
	if err != nil {
		t.Fatal(err)
	}
	if m["GPU-0"] != 2 {
		t.Fatalf("%v", m)
	}
}

func TestCheckDockerGPUOccupancy_SingleTaskExceedsSlots(t *testing.T) {
	host := []string{"GPU-0", "GPU-0"}
	tasks := []model.Task{{TaskID: "a", ResGPU: "GPU-0,GPU-0,GPU-0"}}
	if err := CheckDockerGPUOccupancy(host, tasks); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckDockerGPUOccupancy_TwoSlotsShared(t *testing.T) {
	host := []string{"GPU-0", "GPU-0"}
	tasks := []model.Task{
		{TaskID: "a", ResGPU: "GPU-0"},
		{TaskID: "b", ResGPU: "GPU-0"},
	}
	if err := CheckDockerGPUOccupancy(host, tasks); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDockerGPUOccupancy_ThirdTaskFails(t *testing.T) {
	host := []string{"GPU-0", "GPU-0"}
	tasks := []model.Task{
		{TaskID: "a", ResGPU: "GPU-0"},
		{TaskID: "b", ResGPU: "GPU-0"},
		{TaskID: "c", ResGPU: "GPU-0"},
	}
	if err := CheckDockerGPUOccupancy(host, tasks); err == nil {
		t.Fatal("expected error")
	}
}

func TestRemainingGPUSlots(t *testing.T) {
	hostCap := map[string]int{"GPU-0": 2, "GPU-1": 1}
	used := map[string]int{"GPU-0": 1}
	rem := RemainingGPUSlots(hostCap, used)
	if rem["GPU-0"] != 1 || rem["GPU-1"] != 1 {
		t.Fatalf("%v", rem)
	}
}
