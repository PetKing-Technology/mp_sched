package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
	"mp_sched/internal/provider"
)

// Client 使用本机 / 环境变量 DOCKER_HOST 连接 Docker Engine
type Client struct {
	cli *client.Client
	cfg *config.Docker
}

// New 使用 client.FromEnv（与 docker CLI 一致），cfg 可为 nil
func New(cfg *config.Docker) (*Client, error) {
	if cfg == nil {
		cfg = &config.Docker{}
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Client{cli: cli, cfg: cfg}, nil
}

func (c *Client) Name() string { return "docker" }

// Engine 返回 moby 客户端，供本进程内遥测等扩展使用。
func (c *Client) Engine() *client.Client {
	if c == nil {
		return nil
	}
	return c.cli
}

// ResourceCheck 探测 daemon 可达；若有 business JSON 则校验可解析
func (c *Client) ResourceCheck(ctx context.Context, t *model.Task) (*provider.ResourceCheckResult, error) {
	_, err := c.cli.Ping(ctx)
	if err != nil {
		return &provider.ResourceCheckResult{OK: false, Reason: "docker ping: " + err.Error()}, nil
	}
	if t != nil && len(t.Business) > 0 {
		spec, err := parseBusiness(t.Business)
		if err != nil {
			return &provider.ResourceCheckResult{OK: false, Reason: err.Error()}, nil
		}
		if err := ValidateBusinessPayloadType(&spec); err != nil {
			return &provider.ResourceCheckResult{OK: false, Reason: err.Error()}, nil
		}
	}
	if t != nil {
		spec, _ := parseBusiness(t.Business)
		if im := resolveImage(t, spec); im == "" {
			return &provider.ResourceCheckResult{OK: false, Reason: "docker: set task image or business.image"}, nil
		}
		if r := validateTaskHostResources(t, c.cfg); r != nil {
			return r, nil
		}
		if BusinessPayloadType(&spec) == "env" {
			if strings.TrimSpace(spec.EnvKey) == "" {
				return &provider.ResourceCheckResult{OK: false, Reason: "docker: business.type=env requires business.env_key"}, nil
			}
			return &provider.ResourceCheckResult{OK: true, Reason: "ok"}, nil
		}
		if ConfigFetchByWorker(&spec) {
			if strings.TrimSpace(spec.ConfigOSSKey) == "" {
				return &provider.ResourceCheckResult{OK: false, Reason: "docker: config_mode=worker requires business.config_oss_key (一 task 一 config)"}, nil
			}
			dest := c.configContainerPath(&spec)
			if !strings.HasPrefix(dest, "/") {
				return &provider.ResourceCheckResult{OK: false, Reason: "docker: business.config_container_path must be absolute (or omit to use docker.config_file_container_path)"}, nil
			}
			if c.cfg == nil {
				return &provider.ResourceCheckResult{OK: false, Reason: "docker: missing config"}, nil
			}
			o := c.cfg.OSS
			if !o.Enable {
				return &provider.ResourceCheckResult{OK: false, Reason: "docker: worker 拉配置需要 docker.oss.enable=true"}, nil
			}
			if _, err := newMinioClient(&o); err != nil {
				return &provider.ResourceCheckResult{OK: false, Reason: "oss: " + err.Error()}, nil
			}
		}
	}
	return &provider.ResourceCheckResult{OK: true, Reason: "ok"}, nil
}

// Run 创建并启动容器；runtime_ref 为容器 ID
func (c *Client) Run(ctx context.Context, t *model.Task) (string, error) {
	name, cfg, hostCfg, err := c.runCreateSpec(ctx, t, false)
	if err != nil {
		return "", err
	}
	create, err := c.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, name)
	if err != nil {
		return "", fmt.Errorf("docker create: %w", err)
	}
	if err := c.cli.ContainerStart(ctx, create.ID, container.StartOptions{}); err != nil {
		_ = c.cli.ContainerRemove(ctx, create.ID, container.RemoveOptions{Force: true})
		c.removeStaging(t)
		return "", fmt.Errorf("docker start: %w", err)
	}
	return create.ID, nil
}

func (c *Client) mergeEnv(spec *BusinessSpec) []string {
	if spec == nil {
		return nil
	}
	out := append([]string{}, spec.Env...)
	if BusinessPayloadType(spec) == "env" {
		if k := strings.TrimSpace(spec.EnvKey); k != "" {
			out = append(out, k+"="+spec.EnvValue)
		}
	}
	return out
}

// configContainerPath 本任务 config 在容器内路径：优先 business.config_container_path，否则 docker.config_file_container_path（一 task 一文件、一个挂载点）
func (c *Client) configContainerPath(spec *BusinessSpec) string {
	if spec != nil {
		if p := strings.TrimSpace(spec.ConfigContainerPath); p != "" {
			return p
		}
	}
	if c.cfg != nil {
		if p := strings.TrimSpace(c.cfg.ConfigFileContainerPath); p != "" {
			return p
		}
	}
	return "/etc/mp_sched/config.json"
}

func (c *Client) taskStageDir(taskID string) string {
	base := ""
	if c.cfg != nil {
		base = c.cfg.DataDir
	}
	if base == "" {
		base = filepath.Join(os.TempDir(), "mp_sched-docker")
	}
	return filepath.Join(base, "tasks", taskID)
}

func (c *Client) removeStaging(t *model.Task) {
	if t == nil {
		return
	}
	_ = os.RemoveAll(c.taskStageDir(t.TaskID))
}

func containerName(taskID string) string {
	return "mp-sched-" + strings.ReplaceAll(taskID, ":", "-")
}

func (c *Client) pullImage(ctx context.Context, ref string) error {
	opts := types.ImagePullOptions{}
	if c.cfg != nil && c.cfg.ImagePull.Enable {
		u := strings.TrimSpace(c.cfg.ImagePull.Username)
		tok := strings.TrimSpace(c.cfg.ImagePull.Token)
		if u != "" || tok != "" {
			enc, err := registry.EncodeAuthConfig(registry.AuthConfig{
				Username: u,
				Password: tok,
			})
			if err != nil {
				return fmt.Errorf("registry auth: %w", err)
			}
			opts.RegistryAuth = enc
		}
	}
	out, err := c.cli.ImagePull(ctx, ref, opts)
	if err != nil {
		return err
	}
	defer out.Close()
	_, _ = io.Copy(io.Discard, out)
	return nil
}

// Status 根据容器状态映射终态
func (c *Client) Status(ctx context.Context, t *model.Task) (*provider.RuntimeStatus, error) {
	if t == nil || t.RuntimeRef == "" {
		return &provider.RuntimeStatus{Phase: provider.PhaseUnknown, Message: "no runtime_ref"}, nil
	}
	ins, err := c.cli.ContainerInspect(ctx, t.RuntimeRef)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return &provider.RuntimeStatus{Phase: provider.PhaseFailed, Message: "container not found"}, nil
		}
		return nil, err
	}
	st := ins.State
	if st == nil {
		return &provider.RuntimeStatus{Phase: provider.PhaseUnknown, Message: "no state"}, nil
	}
	if st.Running {
		return &provider.RuntimeStatus{Phase: provider.PhaseRunning, Message: st.Status}, nil
	}
	switch st.Status {
	case "exited":
		if st.ExitCode == 0 {
			return &provider.RuntimeStatus{Phase: provider.PhaseSucceeded, Message: "exited 0"}, nil
		}
		return &provider.RuntimeStatus{
			Phase:   provider.PhaseFailed,
			Message: fmt.Sprintf("exited %d: %s", st.ExitCode, st.Error),
		}, nil
	case "created", "restarting":
		return &provider.RuntimeStatus{Phase: provider.PhaseRunning, Message: st.Status}, nil
	case "dead", "removing":
		return &provider.RuntimeStatus{Phase: provider.PhaseFailed, Message: st.Status}, nil
	default:
		return &provider.RuntimeStatus{Phase: provider.PhaseUnknown, Message: st.Status}, nil
	}
}

// Stop 停止并删除容器（若已删除则忽略），并清理本任务 staging 目录
func (c *Client) Stop(ctx context.Context, t *model.Task) error {
	if t == nil || t.RuntimeRef == "" {
		return nil
	}
	id := t.RuntimeRef
	sec := 10
	if err := c.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &sec}); err != nil {
		if !errdefs.IsNotFound(err) {
			// 未停干净仍尝试 remove
		}
	}
	if err := c.cli.ContainerRemove(ctx, id, container.RemoveOptions{RemoveVolumes: true, Force: true}); err != nil {
		if errdefs.IsNotFound(err) {
			c.removeStaging(t)
			return nil
		}
		return err
	}
	c.removeStaging(t)
	return nil
}

// Close 释放 HTTP 连接（进程退出时可选）
func (c *Client) Close() error {
	if c == nil || c.cli == nil {
		return nil
	}
	return c.cli.Close()
}

var _ provider.Provider = (*Client)(nil)
