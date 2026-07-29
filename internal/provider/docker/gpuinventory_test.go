package docker

import (
	"strings"
	"testing"
	"time"

	"gorm.io/datatypes"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

func TestParseNVIDIASMIInventory(t *testing.T) {
	got, err := ParseNVIDIASMIInventory("0, GPU-a, 46068, 33000\n1, GPU-b, 46068, 12000\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Index != "0" || got[0].UUID != "GPU-a" || got[0].FreeMemoryMB != 33000 {
		t.Fatalf("unexpected inventory: %#v", got)
	}
}

func TestAssignOpportunisticNVIDIADeviceID_UsesMostFreeEligibleGPU(t *testing.T) {
	hr := config.DockerHostResources{GPUIDs: []string{"0", "0", "1"}}
	policy := config.DockerGPUAdmission{
		MinStartFreeMemoryMB: 20 * 1024,
		ReserveMemoryMB:      10 * 1024,
		LaunchGuardSeconds:   120,
	}
	inventory := []GPUInfo{
		{Index: "0", UUID: "GPU-a", TotalMemoryMB: 46068, FreeMemoryMB: 32000},
		{Index: "1", UUID: "GPU-b", TotalMemoryMB: 46068, FreeMemoryMB: 40000},
	}
	id, err := AssignOpportunisticNVIDIADeviceID(
		hr, policy, nil, &model.Task{TaskID: "next", ResGPU: "true"}, inventory, time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if id != "1" {
		t.Fatalf("want GPU 1, got %q", id)
	}
}

func TestAssignOpportunisticNVIDIADeviceID_RequiresReserveOnTopOfStartThreshold(t *testing.T) {
	hr := config.DockerHostResources{GPUIDs: []string{"0"}}
	policy := config.DockerGPUAdmission{
		MinStartFreeMemoryMB: 20 * 1024,
		ReserveMemoryMB:      10 * 1024,
		LaunchGuardSeconds:   120,
	}
	inventory := []GPUInfo{{Index: "0", TotalMemoryMB: 46068, FreeMemoryMB: 30*1024 - 1}}
	_, err := AssignOpportunisticNVIDIADeviceID(
		hr, policy, nil, &model.Task{TaskID: "next", ResGPU: "true"}, inventory, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "required_free_mb=30720") {
		t.Fatalf("expected threshold rejection, got %v", err)
	}
}

func TestAssignOpportunisticNVIDIADeviceID_ObservesLaunchGuard(t *testing.T) {
	now := time.Now().UTC()
	extra, err := ExtraWithAssignedNVIDIAGPUID([]byte("{}"), "0")
	if err != nil {
		t.Fatal(err)
	}
	runningAt := now.Add(-30 * time.Second)
	occupying := []model.Task{{
		TaskID:    "starting",
		ResGPU:    "true",
		Status:    model.TaskStatusRunning,
		Extra:     datatypes.JSON(extra),
		RunningAt: &runningAt,
	}}
	hr := config.DockerHostResources{GPUIDs: []string{"0"}}
	policy := config.DockerGPUAdmission{
		MinStartFreeMemoryMB: 20 * 1024,
		ReserveMemoryMB:      10 * 1024,
		LaunchGuardSeconds:   120,
	}
	inventory := []GPUInfo{{Index: "0", TotalMemoryMB: 46068, FreeMemoryMB: 40000}}
	if _, err := AssignOpportunisticNVIDIADeviceID(
		hr, policy, occupying, &model.Task{TaskID: "next", ResGPU: "true"}, inventory, now,
	); err == nil || !strings.Contains(err.Error(), "launch_guarded=1") {
		t.Fatalf("expected launch guard rejection, got %v", err)
	}
}

func TestAssignOpportunisticNVIDIADeviceID_AdmittedAlwaysGuards(t *testing.T) {
	now := time.Now().UTC()
	extra, _ := ExtraWithAssignedNVIDIAGPUID([]byte("{}"), "0")
	occupying := []model.Task{{
		TaskID:    "pulling",
		ResGPU:    "true",
		Status:    model.TaskStatusAdmitted,
		Extra:     datatypes.JSON(extra),
		UpdatedAt: now.Add(-time.Hour),
	}}
	hr := config.DockerHostResources{GPUIDs: []string{"0"}}
	policy := config.DockerGPUAdmission{
		MinStartFreeMemoryMB: 20 * 1024,
		ReserveMemoryMB:      10 * 1024,
		LaunchGuardSeconds:   120,
	}
	inventory := []GPUInfo{{Index: "0", TotalMemoryMB: 46068, FreeMemoryMB: 40000}}
	if _, err := AssignOpportunisticNVIDIADeviceID(
		hr, policy, occupying, &model.Task{TaskID: "next", ResGPU: "true"}, inventory, now,
	); err == nil {
		t.Fatal("admitted task must guard its assigned GPU until Run finishes")
	}
}

func TestAssignOpportunisticNVIDIADeviceID_LegacyTaskGuardsAllConfiguredGPUs(t *testing.T) {
	now := time.Now().UTC()
	runningAt := now.Add(-time.Hour)
	occupying := []model.Task{{
		TaskID:    "legacy-multi-gpu",
		ResGPU:    "true",
		Status:    model.TaskStatusRunning,
		Extra:     datatypes.JSON(`{}`),
		RunningAt: &runningAt,
	}}
	hr := config.DockerHostResources{GPUIDs: []string{"1", "2"}}
	policy := config.DockerGPUAdmission{
		MinStartFreeMemoryMB: 20 * 1024,
		ReserveMemoryMB:      10 * 1024,
		LaunchGuardSeconds:   120,
	}
	inventory := []GPUInfo{
		{Index: "1", TotalMemoryMB: 46068, FreeMemoryMB: 40000},
		{Index: "2", TotalMemoryMB: 46068, FreeMemoryMB: 45000},
	}
	_, err := AssignOpportunisticNVIDIADeviceID(
		hr, policy, occupying, &model.Task{TaskID: "next", ResGPU: "true"}, inventory, now,
	)
	if err == nil || !strings.Contains(err.Error(), "launch_guarded=2") {
		t.Fatalf("legacy task must guard all configured GPUs, got %v", err)
	}
}
