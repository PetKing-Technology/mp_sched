package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestGpuDeviceRequests_PreservesDuplicateIDs(t *testing.T) {
	req := gpuDeviceRequests("GPU-0, GPU-0 ,GPU-1")
	if len(req) != 1 {
		t.Fatalf("len: %d", len(req))
	}
	want := container.DeviceRequest{
		Driver:       "nvidia",
		Capabilities: [][]string{{"gpu"}},
		DeviceIDs:    []string{"GPU-0", "GPU-0", "GPU-1"},
	}
	if len(req[0].DeviceIDs) != 3 || req[0].Driver != want.Driver {
		t.Fatalf("%+v", req[0])
	}
	for i := range want.DeviceIDs {
		if req[0].DeviceIDs[i] != want.DeviceIDs[i] {
			t.Fatalf("idx %d: got %q want %q", i, req[0].DeviceIDs[i], want.DeviceIDs[i])
		}
	}
}

func TestGpuDeviceRequests_All(t *testing.T) {
	req := gpuDeviceRequests("all")
	if len(req) != 1 || len(req[0].DeviceIDs) != 1 || req[0].DeviceIDs[0] != "all" {
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
