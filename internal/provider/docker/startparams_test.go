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

func TestStartParamsSnapshot_MultiGPUInOneRequest(t *testing.T) {
	t.Parallel()
	dck, _ := New(&config.Docker{
		HostResources: config.DockerHostResources{
			GPUIDs: []string{"GPU-0", "GPU-1"},
		},
	})
	task := &model.Task{
		TaskID:    "22222222-2222-2222-2222-222222222222",
		Provider:  "docker",
		Operation: model.OperationStart,
		Image:     "alpine:3.20",
		ResGPU:    "yes",
		Business:  datatypes.JSON(`{"type":"config","config_mode":"app"}`),
	}
	snap, err := dck.PreviewStartParams(context.Background(), task, PreviewStartParamsOptions{SkipPull: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := snap.Host.DeviceRequests[0].DeviceIDs
	if len(ids) != 2 || ids[0] != "GPU-0" || ids[1] != "GPU-1" {
		t.Fatalf("want [GPU-0 GPU-1], got %v", ids)
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
	// gpu_ids 为多重集，写入容器时按 id 去重，仅挂一次 GPU-0
	if len(ids) != 1 || ids[0] != "GPU-0" {
		t.Fatalf("device_ids: %v", ids)
	}
}
