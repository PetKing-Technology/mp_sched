package docker

// Scheduler-owned compound Docker workloads.  This is intentionally a small
// provider primitive rather than a general compose implementation: one task
// owns one internal bridge network, image-only sidecars and one primary
// container.  The business container never receives docker.sock, host
// networking, published ports, or scheduler global mounts through this path.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"

	"mp_sched/internal/model"
)

const compoundRuntimeVersion = 1

type compoundRuntimeRef struct {
	Version         int      `json:"version"`
	Primary         string   `json:"primary"`
	Network         string   `json:"network"`
	Sidecars        []string `json:"sidecars"`
	LoopbackSidecar string   `json:"loopback_sidecar,omitempty"`
}

func compoundNetworkName(taskID string) string {
	s := sha256.Sum256([]byte(taskID))
	return "mp-sched-net-" + hex.EncodeToString(s[:])[:16]
}

func parseCompoundRuntimeRef(raw string) (*compoundRuntimeRef, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return nil, false, nil
	}
	var ref compoundRuntimeRef
	if err := json.Unmarshal([]byte(raw), &ref); err != nil {
		return nil, true, fmt.Errorf("docker: compound runtime_ref: %w", err)
	}
	if ref.Version != compoundRuntimeVersion || strings.TrimSpace(ref.Primary) == "" || strings.TrimSpace(ref.Network) == "" {
		return nil, true, fmt.Errorf("docker: unsupported compound runtime_ref")
	}
	return &ref, true, nil
}

// PrimaryRuntimeID returns the Docker container id used for logs/stats and
// inspect operations.  Plain runtime refs remain backward compatible.
func PrimaryRuntimeID(raw string) string {
	ref, ok, err := parseCompoundRuntimeRef(raw)
	if ok && err == nil && ref != nil {
		return ref.Primary
	}
	return strings.TrimSpace(raw)
}

func compoundRuntimeJSON(ref *compoundRuntimeRef) (string, error) {
	b, err := json.Marshal(ref)
	if err != nil {
		return "", fmt.Errorf("docker: encode compound runtime_ref: %w", err)
	}
	return string(b), nil
}

func (c *Client) createCompoundNetwork(ctx context.Context, taskID string) (string, error) {
	name := compoundNetworkName(taskID)
	resp, err := c.cli.NetworkCreate(ctx, name, types.NetworkCreate{
		CheckDuplicate: true,
		Driver:         "bridge",
		Internal:       true,
		Attachable:     false,
		Labels: map[string]string{
			"mp_sched.compound":   "true",
			"mp_sched.task_id":    taskID,
			"mp_sched.network_id": name,
		},
	})
	if err != nil {
		return "", fmt.Errorf("docker compound network create: %w", err)
	}
	if strings.TrimSpace(resp.ID) == "" {
		return "", fmt.Errorf("docker compound network create: empty id")
	}
	return resp.ID, nil
}

func compoundNetworking(networkName, alias string) *network.NetworkingConfig {
	return &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
		networkName: {Aliases: []string{alias}},
	}}
}

func (c *Client) runSidecars(ctx context.Context, task *model.Task, spec *CompoundSpec, networkName string) ([]string, error) {
	if spec == nil {
		return nil, nil
	}
	ids := make([]string, 0, len(spec.Sidecars))
	for _, side := range spec.Sidecars {
		image := strings.TrimSpace(side.Image)
		if err := c.pullImage(ctx, image); err != nil {
			return ids, fmt.Errorf("docker compound sidecar %q pull: %w", side.Name, err)
		}
		labels := map[string]string{
			"mp_sched.compound":      "true",
			"mp_sched.compound_role": "sidecar",
			"mp_sched.task_id":       task.TaskID,
			"mp_sched.sidecar_name":  strings.TrimSpace(side.Name),
			"mp_sched.network":       networkName,
		}
		cfg := &container.Config{
			Image:      image,
			Entrypoint: append([]string{}, side.Entrypoint...),
			Cmd:        append([]string{}, side.Command...),
			Env:        append([]string{}, side.Env...),
			WorkingDir: side.Workdir,
			User:       side.User,
			Labels:     labels,
		}
		host := &container.HostConfig{
			NetworkMode: container.NetworkMode(networkName),
			AutoRemove:  true,
			// No mounts, ports, privileged flags, or socket access are exposed
			// by the sidecar contract.
		}
		name := containerName(task.TaskID) + "-sidecar-" + strings.TrimSpace(side.Name)
		created, err := c.cli.ContainerCreate(ctx, cfg, host, compoundNetworking(networkName, side.Name), nil, name)
		if err != nil {
			return ids, fmt.Errorf("docker compound sidecar %q create: %w", side.Name, err)
		}
		if err := c.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
			_ = c.cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
			return ids, fmt.Errorf("docker compound sidecar %q start: %w", side.Name, err)
		}
		ids = append(ids, created.ID)
	}
	return ids, nil
}

func loopbackSidecarID(spec *CompoundSpec, ids []string) (string, string) {
	if spec == nil {
		return "", ""
	}
	for i, side := range spec.Sidecars {
		if side.Loopback && i < len(ids) {
			return strings.TrimSpace(side.Name), ids[i]
		}
	}
	return "", ""
}

func (c *Client) cleanupCompound(ctx context.Context, ref *compoundRuntimeRef, removePrimary bool) error {
	if ref == nil {
		return nil
	}
	var first error
	if removePrimary && strings.TrimSpace(ref.Primary) != "" {
		if err := c.cli.ContainerStop(ctx, ref.Primary, container.StopOptions{}); err != nil && !errdefs.IsNotFound(err) && first == nil {
			first = err
		}
		if err := c.cli.ContainerRemove(ctx, ref.Primary, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !errdefs.IsNotFound(err) && first == nil {
			first = err
		}
	}
	for _, id := range ref.Sidecars {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if err := c.cli.ContainerStop(ctx, id, container.StopOptions{}); err != nil && !errdefs.IsNotFound(err) && first == nil {
			first = err
		}
		if err := c.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !errdefs.IsNotFound(err) && first == nil {
			first = err
		}
	}
	if !removePrimary && ref.LoopbackSidecar == "" && strings.TrimSpace(ref.Primary) != "" && strings.TrimSpace(ref.Network) != "" {
		if err := c.cli.NetworkDisconnect(ctx, ref.Network, ref.Primary, true); err != nil && !errdefs.IsNotFound(err) && first == nil {
			first = err
		}
	}
	if strings.TrimSpace(ref.Network) != "" {
		if err := c.cli.NetworkRemove(ctx, ref.Network); err != nil && !errdefs.IsNotFound(err) && first == nil {
			first = err
		}
	}
	return first
}
