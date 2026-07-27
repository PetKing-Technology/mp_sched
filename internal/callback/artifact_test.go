package callback

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/datatypes"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

func controlledTask(runID string) *model.Task {
	return &model.Task{Business: datatypes.JSON([]byte(`{
  "agent_rt": {"controlled_callback": {
    "run_id": "` + runID + `", "artifact_contract": "sequence_bundle/v1"
  }}
}`))}
}

func writeBundle(t *testing.T, root, runID string, body []byte) string {
	t.Helper()
	path := filepath.Join(root, runID, "output", "sequence_bundle.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCollectSequenceBundleUsesRawBytesDigest(t *testing.T) {
	root := t.TempDir()
	body := []byte("{\"sequence_bundle\": {\"items\": [1]}}\n")
	writeBundle(t, root, "run_01", body)
	got, err := collectSequenceBundle(root, controlledTask("run_01"))
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	sum := sha256.Sum256(body)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if got.Contract != "sequence_bundle/v1" || got.RunID != "run_01" || got.ManifestDigest != want {
		t.Fatalf("binding = %#v, want digest %q", got, want)
	}
}

func TestCollectSequenceBundleRejectsUnsafeOrMalformedOutput(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name  string
		runID string
		body  []byte
	}{
		{name: "missing", runID: "run_02"},
		{name: "unsafe-run-id", runID: "../escape", body: []byte(`{"sequence_bundle": {}}`)},
		{name: "invalid-json", runID: "run_03", body: []byte(`not-json`)},
		{name: "wrong-shape", runID: "run_04", body: []byte(`{"other": {}}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.body != nil && tc.runID != "../escape" {
				writeBundle(t, root, tc.runID, tc.body)
			}
			if _, err := collectSequenceBundle(root, controlledTask(tc.runID)); err == nil {
				t.Fatal("collect unexpectedly succeeded")
			}
		})
	}
}

func TestCollectSequenceBundleRejectsSymlinkAndUnexpectedFile(t *testing.T) {
	root := t.TempDir()
	path := writeBundle(t, root, "run_05", []byte(`{"sequence_bundle": {}}`))
	if err := os.Symlink(path, filepath.Join(root, "run_05", "output", "other.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := collectSequenceBundle(root, controlledTask("run_05")); err == nil {
		t.Fatal("symlinked output unexpectedly accepted")
	}
}

func TestV2SucceededCallbackSignsArtifactBindingOrBoundedError(t *testing.T) {
	root := t.TempDir()
	body := []byte(`{"sequence_bundle": {"answer": 42}}`)
	writeBundle(t, root, "run_06", body)
	srv, captured := captureServer(t)
	defer srv.Close()
	c := New(&config.Callback{Enable: true, URL: srv.URL,
		Auth: config.CallbackAuth{KeyID: "fixture-k1", HMACSecret: "fixture-secret", ArtifactRoot: root}})
	task := controlledTask("run_06")
	task.TaskID, task.Status = "scheduler-job-6", model.TaskStatusSucceeded
	c.Fire(t.Context(), EventSucceeded, task)
	got := captured()
	var payload map[string]any
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if payload["artifact_manifest_digest"] != want {
		t.Fatalf("artifact digest = %#v, want %q", payload["artifact_manifest_digest"], want)
	}
	if payload["artifact_binding_error"] != nil {
		t.Fatalf("unexpected artifact error: %#v", payload["artifact_binding_error"])
	}

	missing := controlledTask("run_07")
	missing.TaskID, missing.Status = "scheduler-job-7", model.TaskStatusSucceeded
	c.Fire(t.Context(), EventSucceeded, missing)
	payload = nil
	if err := json.Unmarshal(captured().body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["artifact_manifest_digest"]; ok {
		t.Fatalf("missing output claimed digest: %#v", payload)
	}
	if payload["artifact_binding_error"] != "unavailable" {
		t.Fatalf("bounded error = %#v", payload["artifact_binding_error"])
	}
}
