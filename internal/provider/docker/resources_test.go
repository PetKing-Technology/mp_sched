package docker

import (
	"testing"

	"mp_sched/internal/config"

	"github.com/docker/docker/api/types/container"
)

func TestGpuDeviceRequestsForTask_Off(t *testing.T) {
	hr := &config.DockerHostResources{GPUIDs: []string{"GPU-0"}}
	if req := gpuDeviceRequestsForTask("false", hr); req != nil {
		t.Fatalf("%#v", req)
	}
}

func TestGpuDeviceRequestsForTask_On(t *testing.T) {
	hr := &config.DockerHostResources{GPUIDs: []string{"GPU-0", "GPU-1"}}
	req := gpuDeviceRequestsForTask("true", hr)
	if len(req) != 1 || len(req[0].DeviceIDs) != 2 {
		t.Fatalf("%+v", req)
	}
	want := container.DeviceRequest{
		Driver:       "nvidia",
		Capabilities: [][]string{{"gpu"}},
		DeviceIDs:    []string{"GPU-0", "GPU-1"},
	}
	if req[0].Driver != want.Driver || len(req[0].DeviceIDs) != 2 {
		t.Fatalf("%+v", req[0])
	}
}

func TestParseMemoryBytes_KubernetesGi(t *testing.T) {
	got, err := parseMemoryBytes("16Gi")
	if err != nil {
		t.Fatal(err)
	}
	want, err := parseMemoryBytes("16GiB")
	if err != nil {
		t.Fatal(err)
	}
	if got != want || got != 16*1024*1024*1024 {
		t.Fatalf("got %d want %d", got, 16*1024*1024*1024)
	}
}

func TestParseMemoryBytes_PreservesGiB(t *testing.T) {
	got, err := parseMemoryBytes("16GiB")
	if err != nil || got != 16*1024*1024*1024 {
		t.Fatalf("got %d err %v", got, err)
	}
}
