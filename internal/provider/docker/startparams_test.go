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

func TestStartParamsSnapshot_PreservesLegacyBusinessMountsAndDockerSocket(t *testing.T) {
	t.Parallel()
	dck, err := New(&config.Docker{Mounts: []config.DockerMount{{Name: "static", HostPath: "/mnt/static", MountPath: "/agent/common"}}})
	if err != nil {
		t.Fatal(err)
	}
	task := &model.Task{TaskID: "44444444-4444-4444-4444-444444444444", Provider: "docker", Image: "alpine:3.20", Business: datatypes.JSON(`{"type":"config","config_mode":"app","mounts":[{"host_path":"/provider/common","container_path":"/agent/common","read_only":true},{"source":{"kind":"host_path","ref":"/provider/task"},"target_path":"/agent/task","writable":true}]}`)}
	snap, err := dck.PreviewStartParams(context.Background(), task, PreviewStartParamsOptions{SkipPull: true})
	if err != nil {
		t.Fatal(err)
	}
	mounts := mountMapByTarget(snap.Host.Mounts)
	if got := mounts["/agent/common"]; got.Source != "/provider/common" || !got.ReadOnly {
		t.Fatalf("business mount did not override static mount: %#v", got)
	}
	if got := mounts["/agent/task"]; got.Source != "/provider/task" || got.ReadOnly {
		t.Fatalf("business writable mount missing: %#v", got)
	}
	if got := mounts["/var/run/docker.sock"]; got.Source != "/var/run/docker.sock" || got.ReadOnly {
		t.Fatalf("legacy writable docker socket missing: %#v", got)
	}
}

func TestStartParamsSnapshot_RejectsUnsupportedLegacyBusinessMountSource(t *testing.T) {
	t.Parallel()
	dck, err := New(&config.Docker{})
	if err != nil {
		t.Fatal(err)
	}
	task := &model.Task{TaskID: "55555555-5555-5555-5555-555555555555", Provider: "docker", Image: "alpine:3.20", Business: datatypes.JSON(`{"type":"config","config_mode":"app","mounts":[{"source":{"kind":"s3","ref":"/bucket/key"},"target_path":"/agent/data"}]}`)}
	_, err = dck.PreviewStartParams(context.Background(), task, PreviewStartParamsOptions{SkipPull: true})
	if err == nil || err.Error() != `docker: business.mounts[0].source.kind must be host_path, got "s3"` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mountMapByTarget(mounts []MountSnapshot) map[string]MountSnapshot {
	out := make(map[string]MountSnapshot, len(mounts))
	for _, item := range mounts {
		out[item.Target] = item
	}
	return out
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
