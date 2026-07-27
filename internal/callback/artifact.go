package callback

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mp_sched/internal/model"
)

var opaqueRunID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var errNoControlledBinding = errors.New("callback artifact: no controlled binding")

type sequenceBundleBinding struct {
	RunID          string
	Contract       string
	ManifestDigest string
}

type taskBusinessBinding struct {
	AgentRT struct {
		ControlledCallback struct {
			RunID            string `json:"run_id"`
			ArtifactContract string `json:"artifact_contract"`
		} `json:"controlled_callback"`
	} `json:"agent_rt"`
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
	output := filepath.Join(rootPath, runID, "output")
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
	body, err := os.ReadFile(artifact)
	if err != nil {
		return sequenceBundleBinding{}, fmt.Errorf("callback artifact: read bundle: %w", err)
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
