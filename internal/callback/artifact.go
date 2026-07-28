package callback

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mp_sched/internal/model"
)

var opaqueRunID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var errNoControlledBinding = errors.New("callback artifact: no controlled binding")

// maxSequenceBundleBytes bounds callback-side memory use. The Runtime contract
// is deliberately a compact manifest, not an unbounded result transport.
const maxSequenceBundleBytes int64 = 8 << 20

type sequenceBundleBinding struct {
	RunID          string
	Contract       string
	ManifestDigest string
}

type taskBusinessBinding struct {
	AgentRT struct {
		ControlledCallback struct {
			TenantID         string `json:"tenant_id"`
			Generation       string `json:"generation"`
			TaskID           string `json:"task_id"`
			WorkUnitID       string `json:"work_unit_id"`
			RunID            string `json:"run_id"`
			Attempt          int    `json:"attempt"`
			FencingToken     string `json:"fencing_token"`
			ArtifactContract string `json:"artifact_contract"`
		} `json:"controlled_callback"`
	} `json:"agent_rt"`
}

// addControlledAuthority copies the non-secret, durable run identity into the
// exact callback body that is subsequently signed.  The shared bridge uses
// these fields as an echo check before it can route a completion.
func addControlledAuthority(body map[string]any, event string, task *model.Task) {
	if task == nil || body == nil {
		return
	}
	var business taskBusinessBinding
	if json.Unmarshal(task.Business, &business) != nil {
		return
	}
	c := business.AgentRT.ControlledCallback
	if c.RunID == "" && c.ArtifactContract == "" {
		return
	}
	body["tenant_id"] = c.TenantID
	body["generation"] = c.Generation
	body["task_id"] = c.TaskID
	body["work_unit_id"] = c.WorkUnitID
	body["run_id"] = c.RunID
	body["attempt"] = c.Attempt
	body["fencing_token"] = c.FencingToken
	body["scheduler_job_id"] = task.TaskID
	body["terminal_status"] = event
}

func collectSequenceBundle(root string, task *model.Task) (sequenceBundleBinding, error) {
	if task == nil {
		return sequenceBundleBinding{}, errors.New("callback artifact: task is required")
	}
	if strings.TrimSpace(root) == "" {
		return sequenceBundleBinding{}, errors.New("callback artifact: root is required")
	}
	var business taskBusinessBinding
	if err := json.Unmarshal(task.Business, &business); err != nil {
		return sequenceBundleBinding{}, fmt.Errorf("callback artifact: binding JSON: %w", err)
	}
	runID := business.AgentRT.ControlledCallback.RunID
	contract := business.AgentRT.ControlledCallback.ArtifactContract
	if runID == "" && contract == "" {
		return sequenceBundleBinding{}, errNoControlledBinding
	}
	if !opaqueRunID.MatchString(runID) {
		return sequenceBundleBinding{}, errors.New("callback artifact: run id is invalid")
	}
	if contract != "sequence_bundle/v1" {
		return sequenceBundleBinding{}, errors.New("callback artifact: contract is invalid")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return sequenceBundleBinding{}, fmt.Errorf("callback artifact: root: %w", err)
	}
	runRoot := filepath.Join(rootPath, runID)
	if err := requireDirectory(runRoot); err != nil {
		return sequenceBundleBinding{}, err
	}
	output := filepath.Join(runRoot, "output")
	if err := requireDirectory(output); err != nil {
		return sequenceBundleBinding{}, err
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		return sequenceBundleBinding{}, fmt.Errorf("callback artifact: read output: %w", err)
	}
	if len(entries) != 1 || entries[0].Name() != "sequence_bundle.json" {
		return sequenceBundleBinding{}, errors.New("callback artifact: output entries are invalid")
	}
	artifact := filepath.Join(output, entries[0].Name())
	info, err := os.Lstat(artifact)
	if err != nil {
		return sequenceBundleBinding{}, fmt.Errorf("callback artifact: inspect bundle: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return sequenceBundleBinding{}, errors.New("callback artifact: bundle is not a regular file")
	}
	file, err := os.Open(artifact)
	if err != nil {
		return sequenceBundleBinding{}, fmt.Errorf("callback artifact: read bundle: %w", err)
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxSequenceBundleBytes+1))
	if err != nil {
		return sequenceBundleBinding{}, fmt.Errorf("callback artifact: read bundle: %w", err)
	}
	if int64(len(body)) > maxSequenceBundleBytes {
		return sequenceBundleBinding{}, errors.New("callback artifact: bundle exceeds maximum size")
	}
	if err := validateSequenceBundle(body); err != nil {
		return sequenceBundleBinding{}, err
	}
	digest := sha256.Sum256(body)
	return sequenceBundleBinding{
		RunID: runID, Contract: "sequence_bundle/v1",
		ManifestDigest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func requireDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("callback artifact: output unavailable: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("callback artifact: output is not a directory")
	}
	return nil
}

func validateSequenceBundle(body []byte) error {
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return fmt.Errorf("callback artifact: sequence bundle JSON: %w", err)
	}
	bundle, ok := value["sequence_bundle"].(map[string]any)
	if !ok || bundle == nil {
		return errors.New("callback artifact: sequence bundle shape is invalid")
	}
	return nil
}
