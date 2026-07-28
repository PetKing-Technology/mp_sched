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
	Mounts      []TaskMount `json:"mounts"`

	// 以下为 type=config（或空，默认）时使用
	ConfigOSSKey        string `json:"config_oss_key"`
	ConfigMode          string `json:"config_mode"`
	ConfigContainerPath string `json:"config_container_path"`

	// 以下为 type=env 时使用
	EnvKey   string `json:"env_key"`
	EnvValue string `json:"env_value"`
}

// TaskMount is an explicit per-task bind mount.  It is intentionally kept
// separate from the scheduler's global Docker mounts so a completion artifact
// can be written into the exact host directory registered by Runtime.
type TaskMount struct {
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
	ReadOnly      bool   `json:"read_only"`
}

func parseBusiness(raw []byte) (BusinessSpec, error) {
	if len(raw) == 0 {
		return BusinessSpec{}, nil
	}
	var s BusinessSpec
	if err := json.Unmarshal(raw, &s); err != nil {
		return BusinessSpec{}, fmt.Errorf("docker: business json: %w", err)
	}
	return s, nil
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
