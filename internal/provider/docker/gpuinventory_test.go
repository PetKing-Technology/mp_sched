package docker

import (
	"strings"
	"testing"
	"time"

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

func TestAssignOpportunisticNVIDIADeviceID_AdmitsAtConfiguredFreeMemoryThreshold(t *testing.T) {
	hr := config.DockerHostResources{GPUIDs: []string{"0"}}
	policy := config.DockerGPUAdmission{
		MinStartFreeMemoryMB: 20 * 1024,
		ReserveMemoryMB:      10 * 1024,
		LaunchGuardSeconds:   120,
	}
	inventory := []GPUInfo{{Index: "0", TotalMemoryMB: 46068, FreeMemoryMB: 20 * 1024}}
	id, err := AssignOpportunisticNVIDIADeviceID(
		hr, policy, nil, &model.Task{TaskID: "next", ResGPU: "true"}, inventory, time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if id != "0" {
		t.Fatalf("want GPU 0, got %q", id)
	}
}

func TestAssignOpportunisticNVIDIADeviceID_RejectsBelowConfiguredFreeMemoryThreshold(t *testing.T) {
	hr := config.DockerHostResources{GPUIDs: []string{"0"}}
	policy := config.DockerGPUAdmission{MinStartFreeMemoryMB: 20 * 1024}
	inventory := []GPUInfo{{Index: "0", TotalMemoryMB: 46068, FreeMemoryMB: 20*1024 - 1}}
	_, err := AssignOpportunisticNVIDIADeviceID(
		hr, policy, nil, &model.Task{TaskID: "next", ResGPU: "true"}, inventory, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "required_free_mb=20480") {
		t.Fatalf("expected threshold rejection, got %v", err)
	}
}

func TestAssignOpportunisticNVIDIADeviceID_IgnoresRecentRunningAssignment(t *testing.T) {
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
		Extra:     extra,
		RunningAt: &runningAt,
	}}
	hr := config.DockerHostResources{GPUIDs: []string{"0"}}
	policy := config.DockerGPUAdmission{
		MinStartFreeMemoryMB: 20 * 1024,
		ReserveMemoryMB:      10 * 1024,
		LaunchGuardSeconds:   120,
	}
	inventory := []GPUInfo{{Index: "0", TotalMemoryMB: 46068, FreeMemoryMB: 40000}}
	id, err := AssignOpportunisticNVIDIADeviceID(
		hr, policy, occupying, &model.Task{TaskID: "next", ResGPU: "true"}, inventory, now,
	)
	if err != nil || id != "0" {
		t.Fatalf("nominal running assignment must not block free GPU: id=%q err=%v", id, err)
	}
}

func TestAssignOpportunisticNVIDIADeviceID_IgnoresAdmittedAssignment(t *testing.T) {
	now := time.Now().UTC()
	extra, _ := ExtraWithAssignedNVIDIAGPUID([]byte("{}"), "0")
	occupying := []model.Task{{
		TaskID:    "pulling",
		ResGPU:    "true",
		Status:    model.TaskStatusAdmitted,
		Extra:     extra,
		UpdatedAt: now.Add(-time.Hour),
	}}
	hr := config.DockerHostResources{GPUIDs: []string{"0"}}
	policy := config.DockerGPUAdmission{
		MinStartFreeMemoryMB: 20 * 1024,
		ReserveMemoryMB:      10 * 1024,
		LaunchGuardSeconds:   120,
	}
	inventory := []GPUInfo{{Index: "0", TotalMemoryMB: 46068, FreeMemoryMB: 40000}}
	id, err := AssignOpportunisticNVIDIADeviceID(
		hr, policy, occupying, &model.Task{TaskID: "next", ResGPU: "true"}, inventory, now,
	)
	if err != nil || id != "0" {
		t.Fatalf("nominal admitted assignment must not block free GPU: id=%q err=%v", id, err)
	}
}

func TestAssignOpportunisticNVIDIADeviceID_IgnoresLegacyMultiGPUNominalOccupancy(t *testing.T) {
	now := time.Now().UTC()
	runningAt := now.Add(-time.Hour)
	occupying := []model.Task{{
		TaskID:    "legacy-multi-gpu",
		ResGPU:    "true",
		Status:    model.TaskStatusRunning,
		Extra:     []byte(`{}`),
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
	id, err := AssignOpportunisticNVIDIADeviceID(
		hr, policy, occupying, &model.Task{TaskID: "next", ResGPU: "true"}, inventory, now,
	)
	if err != nil || id != "2" {
		t.Fatalf("legacy nominal occupancy must not block freer GPU 2: id=%q err=%v", id, err)
	}
}
