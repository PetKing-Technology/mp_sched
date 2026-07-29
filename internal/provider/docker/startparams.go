package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

// StartParamsSnapshot 与 ContainerCreate 前一刻一致的可 JSON 化视图（用于排查/单测，不执行创建）。
type StartParamsSnapshot struct {
	ContainerName string             `json:"container_name"`
	Image         string             `json:"image"`
	Config        ConfigSnapshot     `json:"config"`
	Host          HostConfigSnapshot `json:"host_config"`
	RawConfigJSON json.RawMessage    `json:"raw_container_config_json,omitempty"`
	RawHostJSON   json.RawMessage    `json:"raw_host_config_json,omitempty"`
}

// ConfigSnapshot 对应 container.Config 中我们会设置的主要字段。
type ConfigSnapshot struct {
	Entrypoint []string          `json:"entrypoint,omitempty"`
	Cmd        []string          `json:"cmd,omitempty"`
	Env        []string          `json:"env,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	User       string            `json:"user,omitempty"`
	Hostname   string            `json:"hostname,omitempty"`
}

// HostConfigSnapshot 对应 host 上挂资源/挂载/网络。
type HostConfigSnapshot struct {
	NanoCPUs       int64                   `json:"nano_cpus,omitempty"`
	Memory         int64                   `json:"memory_bytes,omitempty"`
	DeviceRequests []DeviceRequestSnapshot `json:"device_requests,omitempty"`
	// ResGPU 任务侧「是否需要 GPU」；具体 device id 来自 [docker.host_resources]
	ResGPU      string          `json:"res_gpu_input,omitempty"`
	Mounts      []MountSnapshot `json:"mounts,omitempty"`
	NetworkMode string          `json:"network_mode,omitempty"`
	AutoRemove  bool            `json:"auto_remove,omitempty"`
}

// DeviceRequestSnapshot 来自 moby container.DeviceRequest 的易读子集。
type DeviceRequestSnapshot struct {
	Driver       string     `json:"driver,omitempty"`
	DeviceIDs    []string   `json:"device_ids,omitempty"`
	Capabilities [][]string `json:"capabilities,omitempty"`
}

// MountSnapshot bind mount 摘要。
type MountSnapshot struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

// PreviewStartParamsOptions 控制 PreviewStartParams 行为；零值 = 与 Run 前准备一致，含 pull/含 OSS 挂载。
type PreviewStartParamsOptions struct {
	// SkipPull 为 true 时不拉镜像（仅本地排查/无 daemon 的单元测试时配合假 task 使用）
	SkipPull bool
}

// PreviewStartParams 组装与即将 ContainerCreate 相同的数据并转为快照，不调用 ContainerCreate/ContainerStart。
// 与 Run 共用 runCreateSpec；需要 Docker 与（若启用）OSS 时仍会访问网络，除非用简单任务+SkipPull 做纯构参单测见 TestStartParamsSnapshot_* 。
func (c *Client) PreviewStartParams(ctx context.Context, t *model.Task, opt PreviewStartParamsOptions) (*StartParamsSnapshot, error) {
	if c == nil {
		return nil, fmt.Errorf("docker: nil client")
	}
	name, cfg, hostCfg, err := c.runCreateSpec(ctx, t, opt.SkipPull)
	if err != nil {
		return nil, err
	}
	snap := snapshotFromMoby(name, t, cfg, hostCfg)
	rc, _ := json.MarshalIndent(cfg, "", "  ")
	rh, _ := json.MarshalIndent(hostCfg, "", "  ")
	snap.RawConfigJSON = rc
	snap.RawHostJSON = rh
	return snap, nil
}

func snapshotFromMoby(name string, t *model.Task, cfg *container.Config, hostCfg *container.HostConfig) *StartParamsSnapshot {
	if cfg == nil {
		cfg = &container.Config{}
	}
	if hostCfg == nil {
		hostCfg = &container.HostConfig{}
	}
	s := &StartParamsSnapshot{
		ContainerName: name,
		Image:         cfg.Image,
		Config: ConfigSnapshot{
			Entrypoint: append([]string{}, cfg.Entrypoint...),
			Cmd:        append([]string{}, cfg.Cmd...),
			Env:        append([]string{}, cfg.Env...),
			Labels:     copyLabels(cfg.Labels),
			WorkingDir: cfg.WorkingDir,
			User:       cfg.User,
			Hostname:   cfg.Hostname,
		},
		Host: HostConfigSnapshot{
			ResGPU:         "",
			Mounts:         mountsSnapshot(hostCfg.Mounts),
			NetworkMode:    string(hostCfg.NetworkMode),
			AutoRemove:     hostCfg.AutoRemove,
			DeviceRequests: deviceReqSnapshot(hostCfg.Resources.DeviceRequests),
		},
	}
	if t != nil {
		s.Host.ResGPU = t.ResGPU
	}
	if hostCfg.Resources.NanoCPUs > 0 {
		s.Host.NanoCPUs = hostCfg.Resources.NanoCPUs
	}
	if hostCfg.Resources.Memory > 0 {
		s.Host.Memory = hostCfg.Resources.Memory
	}
	return s
}

func copyLabels(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func deviceReqSnapshot(in []container.DeviceRequest) []DeviceRequestSnapshot {
	if len(in) == 0 {
		return nil
	}
	out := make([]DeviceRequestSnapshot, 0, len(in))
	for _, d := range in {
		ids := append([]string{}, d.DeviceIDs...)
		caps := make([][]string, 0, len(d.Capabilities))
		for _, c := range d.Capabilities {
			caps = append(caps, append([]string{}, c...))
		}
		out = append(out, DeviceRequestSnapshot{
			Driver:       d.Driver,
			DeviceIDs:    ids,
			Capabilities: caps,
		})
	}
	return out
}

func mountsSnapshot(in []mount.Mount) []MountSnapshot {
	if len(in) == 0 {
		return nil
	}
	out := make([]MountSnapshot, 0, len(in))
	for _, m := range in {
		out = append(out, MountSnapshot{
			Type:     string(m.Type),
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
	return out
}

// runCreateSpec 与 Run 在 ContainerCreate 前一致。skipPull 为 true 时不拉镜像（供 PreviewStartParams / 无 daemon 单测小任务使用）。
func (c *Client) runCreateSpec(ctx context.Context, t *model.Task, skipPull bool) (name string, cfg *container.Config, hostCfg *container.HostConfig, err error) {
	if c == nil {
		return "", nil, nil, fmt.Errorf("docker: nil client")
	}
	if t == nil {
		return "", nil, nil, fmt.Errorf("docker: nil task")
	}
	spec, err := parseBusiness(t.Business)
	if err != nil {
		return "", nil, nil, err
	}
	image := resolveImage(t, spec)
	if image == "" {
		return "", nil, nil, fmt.Errorf("docker: no image (set task.image or business.image)")
	}
	if !skipPull {
		if err := c.pullImage(ctx, image); err != nil {
			return "", nil, nil, fmt.Errorf("docker pull: %w", err)
		}
	}
	var mnts []mount.Mount
	if c.cfg != nil {
		for _, m := range c.cfg.Mounts {
			h := strings.TrimSpace(m.HostPath)
			p := strings.TrimSpace(m.MountPath)
			if h == "" || p == "" {
				continue
			}
			mnts = append(mnts, mount.Mount{
				Type:     mount.TypeBind,
				Source:   h,
				Target:   p,
				ReadOnly: false,
			})
		}
	}
	for _, m := range spec.Mounts {
		h := strings.TrimSpace(m.HostPath)
		p := strings.TrimSpace(m.ContainerPath)
		if h == "" || p == "" || !filepath.IsAbs(h) || !filepath.IsAbs(p) {
			return "", nil, nil, fmt.Errorf("docker: task mount paths must be absolute")
		}
		mnts = append(mnts, mount.Mount{
			Type: mount.TypeBind, Source: h, Target: p, ReadOnly: m.ReadOnly,
		})
	}
	if ConfigFetchByWorker(&spec) && strings.TrimSpace(spec.ConfigOSSKey) != "" {
		local, err := c.downloadFromOSS(ctx, t.TaskID, spec.ConfigOSSKey)
		if err != nil {
			return "", nil, nil, fmt.Errorf("oss download: %w", err)
		}
		mnts = append(mnts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   local,
			Target:   c.configContainerPath(&spec),
			ReadOnly: true,
		})
	}
	nano, err := parseNanoCPUs(t.ResCPU)
	if err != nil {
		return "", nil, nil, err
	}
	mem, err := parseMemoryBytes(t.ResMemory)
	if err != nil {
		return "", nil, nil, err
	}
	var hr *config.DockerHostResources
	if c.cfg != nil {
		hr = &c.cfg.HostResources
	}
	gpuID := AssignedNVIDIAGPUID(t)
	if gpuID == "" && TaskWantsGPU(t.ResGPU) && hr != nil {
		ids, _ := ResolveNVIDIADeviceIDsForAttach(hr)
		if len(ids) > 0 {
			gpuID = ids[0]
		}
	}
	hostCfg = &container.HostConfig{
		Resources: container.Resources{
			NanoCPUs:       nano,
			Memory:         mem,
			DeviceRequests: gpuDeviceRequestsForTask(t.ResGPU, gpuID),
		},
		Mounts:     mnts,
		AutoRemove: spec.AutoRemove,
	}
	if nm := strings.TrimSpace(spec.NetworkMode); nm != "" {
		hostCfg.NetworkMode = container.NetworkMode(nm)
	}
	labels := map[string]string{
		"mp_sched.task_id":  t.TaskID,
		"mp_sched.provider": "docker",
		"vendor":            "mova",
	}
	cfg = &container.Config{
		Image:      image,
		Labels:     labels,
		WorkingDir: spec.Workdir,
		User:       spec.User,
		Hostname:   spec.Hostname,
		Env:        c.mergeEnv(&spec),
	}
	if len(spec.Entrypoint) > 0 {
		cfg.Entrypoint = spec.Entrypoint
	}
	if len(spec.Command) > 0 {
		cfg.Cmd = spec.Command
	}
	name = containerName(t.TaskID)
	return name, cfg, hostCfg, nil
}
