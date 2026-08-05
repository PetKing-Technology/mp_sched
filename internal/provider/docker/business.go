package docker

import (
	"encoding/json"
	"fmt"
	"strings"

	"mp_sched/internal/model"
)

// BusinessSpec 从 task.business JSON 解析；镜像以 task.image 为主，此处 image 仅作兼容回退
type BusinessSpec struct {
	// Type 传参方式：config=配置文件/OSS 分支；env=用 env_key + env_value 注入容器（不注入整段 business JSON）
	PayloadType string   `json:"type"`
	Image       string   `json:"image"`
	Command     []string `json:"command"`
	Entrypoint  []string `json:"entrypoint"`
	Env         []string `json:"env"`
	Workdir     string   `json:"workdir"`
	User        string   `json:"user"`
	Hostname    string   `json:"hostname"`
	NetworkMode string   `json:"network_mode"`
	AutoRemove  bool     `json:"auto_remove"`
	// ShmSizeBytes is an explicit, task-owned Docker /dev/shm allocation.
	// Zero preserves Docker's default; a negative value is rejected before
	// ContainerCreate rather than being silently normalized.
	ShmSizeBytes int64 `json:"shm_size_bytes"`

	// 以下为 type=config（或空，默认）时使用
	ConfigOSSKey        string `json:"config_oss_key"`
	ConfigMode          string `json:"config_mode"`
	ConfigContainerPath string `json:"config_container_path"`

	// Compound optionally asks the Docker provider to create a scheduler-owned
	// private bridge network, launch the declared sidecars on that network, and
	// attach the primary task container to it.  Sidecars deliberately have no
	// mount/socket fields: they are image-only service helpers and cannot gain
	// access to the host Docker socket or scheduler workspace by accident.
	Compound *CompoundSpec `json:"compound,omitempty"`

	// 以下为 type=env 时使用
	EnvKey   string `json:"env_key"`
	EnvValue string `json:"env_value"`
}

// CompoundSpec is the closed, scheduler-native sidecar contract.  The
// network name is generated from the task id (the field is intentionally not
// user supplied); aliases are the only way for the primary container to reach
// a sidecar.  All containers remain on an internal bridge network.
type CompoundSpec struct {
	Sidecars []SidecarSpec `json:"sidecars"`
}

// SidecarSpec contains only immutable image/process identity.  Mounts,
// network mode, ports and privileged flags are intentionally not representable.
type SidecarSpec struct {
	Name  string `json:"name"`
	Image string `json:"image"`
	// Loopback makes the primary share this sidecar's network namespace
	// (container:<sidecar-id>), allowing fixed localhost APIs such as
	// 127.0.0.1:9100 without host networking or published ports.
	Loopback   bool     `json:"loopback,omitempty"`
	Entrypoint []string `json:"entrypoint,omitempty"`
	Command    []string `json:"command,omitempty"`
	Env        []string `json:"env,omitempty"`
	Workdir    string   `json:"workdir,omitempty"`
	User       string   `json:"user,omitempty"`
}

func parseBusiness(raw []byte) (BusinessSpec, error) {
	if len(raw) == 0 {
		return BusinessSpec{}, nil
	}
	var s BusinessSpec
	if err := json.Unmarshal(raw, &s); err != nil {
		return BusinessSpec{}, fmt.Errorf("docker: business json: %w", err)
	}
	if err := validateCompound(&s); err != nil {
		return BusinessSpec{}, err
	}
	return s, nil
}

func validateCompound(spec *BusinessSpec) error {
	if spec == nil || spec.Compound == nil {
		return nil
	}
	if len(spec.Compound.Sidecars) == 0 {
		return fmt.Errorf("docker: compound.sidecars must not be empty")
	}
	seen := make(map[string]struct{}, len(spec.Compound.Sidecars))
	loopback := 0
	for i, s := range spec.Compound.Sidecars {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			return fmt.Errorf("docker: compound.sidecars[%d].name is required", i)
		}
		if !validCompoundName(name) {
			return fmt.Errorf("docker: compound.sidecars[%d].name must be DNS-safe", i)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("docker: duplicate compound sidecar name %q", name)
		}
		seen[name] = struct{}{}
		image := strings.TrimSpace(s.Image)
		if image == "" {
			return fmt.Errorf("docker: compound.sidecars[%d].image is required", i)
		}
		if !strings.Contains(image, "@sha256:") {
			return fmt.Errorf("docker: compound.sidecars[%d].image must be digest-pinned", i)
		}
		if s.Loopback {
			loopback++
		}
	}
	if loopback > 1 {
		return fmt.Errorf("docker: compound allows at most one loopback sidecar")
	}
	if n := strings.ToLower(strings.TrimSpace(spec.NetworkMode)); n == "host" || strings.HasPrefix(n, "container:") {
		return fmt.Errorf("docker: compound tasks cannot use network_mode=%q", spec.NetworkMode)
	}
	return nil
}

func validCompoundName(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (i > 0 && (r == '-' || r == '_' || r == '.')) {
			continue
		}
		return false
	}
	return true
}

// BusinessPayloadType 归一化 type：config（含 file/空）或 env
func BusinessPayloadType(spec *BusinessSpec) string {
	if spec == nil {
		return "config"
	}
	t := strings.ToLower(strings.TrimSpace(spec.PayloadType))
	if t == "" || t == "config" || t == "file" {
		return "config"
	}
	if t == "env" {
		return "env"
	}
	return t
}

// ValidateBusinessPayloadType 不合法时返回 error
func ValidateBusinessPayloadType(spec *BusinessSpec) error {
	if spec == nil {
		return nil
	}
	t := strings.ToLower(strings.TrimSpace(spec.PayloadType))
	if t == "" || t == "config" || t == "file" || t == "env" {
		return nil
	}
	return fmt.Errorf("docker: business.type must be config, file, or env, got %q", spec.PayloadType)
}

// ConfigFetchByWorker 是否由 worker 从 OSS 下载并 bind（仅 type=config 且 config_mode=worker）
func ConfigFetchByWorker(spec *BusinessSpec) bool {
	if spec == nil {
		return false
	}
	if BusinessPayloadType(spec) == "env" {
		return false
	}
	s := strings.ToLower(strings.TrimSpace(spec.ConfigMode))
	return s == "worker" || s == "scheduler"
}

func resolveImage(t *model.Task, spec BusinessSpec) string {
	if t != nil {
		if s := strings.TrimSpace(t.Image); s != "" {
			return s
		}
	}
	if s := strings.TrimSpace(spec.Image); s != "" {
		return s
	}
	return ""
}
