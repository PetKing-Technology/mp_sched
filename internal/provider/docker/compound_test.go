package docker

import (
	"context"
	"strings"
	"testing"

	"gorm.io/datatypes"
	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

func TestParseBusinessCompoundClosedContract(t *testing.T) {
	task := &model.Task{
		TaskID:   "compound-contract",
		Business: datatypes.JSON(`{"compound":{"sidecars":[{"name":"askcos-app","image":"app@sha256:abc","command":["python","-m","server"]}]}}`),
	}
	spec, err := parseBusiness(task.Business)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Compound == nil || len(spec.Compound.Sidecars) != 1 {
		t.Fatalf("compound: %#v", spec.Compound)
	}
	if got := compoundNetworkName(task.TaskID); !strings.HasPrefix(got, "mp-sched-net-") {
		t.Fatalf("network name: %q", got)
	}
}

func TestParseBusinessCompoundRejectsHostNetwork(t *testing.T) {
	_, err := parseBusiness([]byte(`{"network_mode":"host","compound":{"sidecars":[{"name":"app","image":"app@sha256:abc"}]}}`))
	if err == nil || !strings.Contains(err.Error(), "cannot use network_mode") {
		t.Fatalf("want host-network rejection, got %v", err)
	}
}

func TestParseBusinessCompoundRejectsDuplicateOrMissingSidecarIdentity(t *testing.T) {
	for _, raw := range []string{
		`{"compound":{"sidecars":[]}}`,
		`{"compound":{"sidecars":[{"name":"app","image":"a"},{"name":"app","image":"b"}]}}`,
		`{"compound":{"sidecars":[{"name":"app"}]}}`,
	} {
		if _, err := parseBusiness([]byte(raw)); err == nil {
			t.Fatalf("expected rejection for %s", raw)
		}
	}
	if _, err := parseBusiness([]byte(`{"compound":{"sidecars":[{"name":"a","image":"a","loopback":true},{"name":"b","image":"b","loopback":true}]}}`)); err == nil {
		t.Fatal("expected duplicate loopback rejection")
	}
}

func TestLoopbackSidecarIDMapsToDeclaredSidecar(t *testing.T) {
	spec := &CompoundSpec{Sidecars: []SidecarSpec{
		{Name: "app", Image: "app@sha256:abc", Loopback: true},
		{Name: "expand", Image: "expand@sha256:def"},
	}}
	name, id := loopbackSidecarID(spec, []string{"cid-app", "cid-expand"})
	if name != "app" || id != "cid-app" {
		t.Fatalf("loopback mapping: %q %q", name, id)
	}
}

func TestPrimaryRuntimeIDAcceptsCompoundAndLegacyRefs(t *testing.T) {
	raw, err := compoundRuntimeJSON(&compoundRuntimeRef{Version: compoundRuntimeVersion, Primary: "cid-primary", Network: "nid", Sidecars: []string{"cid-app"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := PrimaryRuntimeID(raw); got != "cid-primary" {
		t.Fatalf("compound primary: %q", got)
	}
	if got := PrimaryRuntimeID("cid-legacy"); got != "cid-legacy" {
		t.Fatalf("legacy primary: %q", got)
	}
}

func TestPreviewCompoundRejectsDockerSocketMount(t *testing.T) {
	dck, err := New(&config.Docker{Mounts: []config.DockerMount{{
		Name: "socket", HostPath: "/var/run/docker.sock", MountPath: "/var/run/docker.sock",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = dck.PreviewStartParams(context.Background(), &model.Task{
		TaskID: "compound-socket", Image: "mcts@sha256:abc",
		Business: datatypes.JSON(`{"compound":{"sidecars":[{"name":"app","image":"app@sha256:def"}]}}`),
	}, PreviewStartParamsOptions{SkipPull: true})
	if err == nil || !strings.Contains(err.Error(), "cannot mount docker socket") {
		t.Fatalf("want socket rejection, got %v", err)
	}
}
