package docker

import (
	"context"
	"encoding/json"
	"testing"

	"gorm.io/datatypes"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

func TestStartParamsSnapshot_SingleGPU(t *testing.T) {
	t.Parallel()
	dck, err := New(&config.Docker{
		HostResources: config.DockerHostResources{
			GPUIDs: []string{"GPU-0"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	task := &model.Task{
		TaskID:    "11111111-1111-1111-1111-111111111111",
		Provider:  "docker",
		Operation: model.OperationStart,
		Image:     "alpine:3.20",
		ResCPU:    "1",
		ResMemory: "128M",
		ResGPU:    "true",
		Business:  datatypes.JSON(`{"type":"config","config_mode":"app"}`),
	}
	snap, err := dck.PreviewStartParams(ctx, task, PreviewStartParamsOptions{SkipPull: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Host.DeviceRequests) != 1 {
		t.Fatalf("want 1 device request, got %#v", snap.Host.DeviceRequests)
	}
	ids := snap.Host.DeviceRequests[0].DeviceIDs
	if len(ids) != 1 || ids[0] != "GPU-0" {
		t.Fatalf("device_ids: %v", ids)
	}
	if snap.Host.ResGPU != "true" {
		t.Fatalf("ResGPU: %q", snap.Host.ResGPU)
	}
	b, _ := json.MarshalIndent(snap, "", "  ")
	t.Logf("PreviewStartParams (SkipPull, 单卡):\n%s", string(b))
}

func TestStartParamsSnapshot_UsesAssignedSingleGPU(t *testing.T) {
	t.Parallel()
	dck, _ := New(&config.Docker{
		HostResources: config.DockerHostResources{
			GPUIDs: []string{"GPU-0", "GPU-1"},
		},
	})
	extra, err := ExtraWithAssignedNVIDIAGPUID([]byte(`{}`), "GPU-1")
	if err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		TaskID:    "22222222-2222-2222-2222-222222222222",
		Provider:  "docker",
		Operation: model.OperationStart,
		Image:     "alpine:3.20",
		ResGPU:    "yes",
		Business:  datatypes.JSON(`{"type":"config","config_mode":"app"}`),
		Extra:     datatypes.JSON(extra),
	}
	snap, err := dck.PreviewStartParams(context.Background(), task, PreviewStartParamsOptions{SkipPull: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := snap.Host.DeviceRequests[0].DeviceIDs
	if len(ids) != 1 || ids[0] != "GPU-1" {
		t.Fatalf("want only assigned GPU-1, got %v", ids)
	}
	t.Logf("device_ids: %v", ids)
}

func TestStartParamsSnapshot_DedupedDeviceIDsFromMultiset(t *testing.T) {
	t.Parallel()
	dck, _ := New(&config.Docker{
		HostResources: config.DockerHostResources{
			GPUIDs: []string{"GPU-0", "GPU-0"},
		},
	})
	task := &model.Task{
		TaskID:    "33333333-3333-3333-3333-333333333333",
		Provider:  "docker",
		Operation: model.OperationStart,
		Image:     "alpine:3.20",
		ResGPU:    "1",
		Business:  datatypes.JSON(`{"type":"config","config_mode":"app"}`),
	}
	snap, err := dck.PreviewStartParams(context.Background(), task, PreviewStartParamsOptions{SkipPull: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := snap.Host.DeviceRequests[0].DeviceIDs
	// 没有 Extra 分配结果时兼容旧任务，回退到配置中的第一个槽位，仍只挂一张卡。
	if len(ids) != 1 || ids[0] != "GPU-0" {
		t.Fatalf("device_ids: %v", ids)
	}
}

func TestStartParamsSnapshot_UsesTaskArtifactMount(t *testing.T) {
	t.Parallel()
	dck, err := New(&config.Docker{})
	if err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		TaskID:    "44444444-4444-4444-4444-444444444444",
		Provider:  "docker",
		Operation: model.OperationStart,
		Image:     "alpine:3.20",
		Business:  datatypes.JSON(`{"mounts":[{"host_path":"/var/lib/mpai-zymctrl/output/run-1","container_path":"/job","read_only":false}]}`),
	}
	snap, err := dck.PreviewStartParams(context.Background(), task, PreviewStartParamsOptions{SkipPull: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Host.Mounts) != 1 {
		t.Fatalf("want one task mount, got %#v", snap.Host.Mounts)
	}
	got := snap.Host.Mounts[0]
	if got.Source != "/var/lib/mpai-zymctrl/output/run-1" || got.Target != "/job" || got.ReadOnly {
		t.Fatalf("unexpected task mount: %#v", got)
	}
}

func TestStartParamsSnapshot_RejectsRelativeTaskArtifactMount(t *testing.T) {
	t.Parallel()
	dck, _ := New(&config.Docker{})
	task := &model.Task{
		TaskID:    "55555555-5555-5555-5555-555555555555",
		Provider:  "docker",
		Operation: model.OperationStart,
		Image:     "alpine:3.20",
		Business:  datatypes.JSON(`{"mounts":[{"host_path":"relative-output","container_path":"/job"}]}`),
	}
	_, err := dck.PreviewStartParams(context.Background(), task, PreviewStartParamsOptions{SkipPull: true})
	if err == nil {
		t.Fatal("want relative task mount rejection")
	}
}
