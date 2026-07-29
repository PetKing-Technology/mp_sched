package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

// GPUInfo 是 nvidia-smi 返回的单卡显存快照，显存单位为 MiB。
type GPUInfo struct {
	Index         string
	UUID          string
	TotalMemoryMB int64
	FreeMemoryMB  int64
}

// QueryNVIDIAGPUs 获取当前宿主机 GPU 的真实空闲显存。查询失败时机会式准入采用 fail-closed。
func QueryNVIDIAGPUs(ctx context.Context, timeout time.Duration) ([]GPUInfo, error) {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(qctx, "nvidia-smi",
		"--query-gpu=index,uuid,memory.total,memory.free",
		"--format=csv,noheader,nounits",
	)
	out, err := cmd.Output()
	if err != nil {
		if qctx.Err() != nil {
			return nil, fmt.Errorf("docker: nvidia-smi query timeout: %w", qctx.Err())
		}
		return nil, fmt.Errorf("docker: nvidia-smi query failed: %w", err)
	}
	gpus, err := ParseNVIDIASMIInventory(string(out))
	if err != nil {
		return nil, err
	}
	return gpus, nil
}

// ParseNVIDIASMIInventory 解析 index,uuid,total,free 四列 CSV。
func ParseNVIDIASMIInventory(raw string) ([]GPUInfo, error) {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	out := make([]GPUInfo, 0, len(lines))
	for lineNo, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) != 4 {
			return nil, fmt.Errorf("docker: invalid nvidia-smi row %d: %q", lineNo+1, line)
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		total, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || total <= 0 {
			return nil, fmt.Errorf("docker: invalid GPU total memory in row %d: %q", lineNo+1, parts[2])
		}
		free, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil || free < 0 {
			return nil, fmt.Errorf("docker: invalid GPU free memory in row %d: %q", lineNo+1, parts[3])
		}
		out = append(out, GPUInfo{
			Index:         parts[0],
			UUID:          parts[1],
			TotalMemoryMB: total,
			FreeMemoryMB:  free,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("docker: nvidia-smi returned no GPUs")
	}
	return out, nil
}

func uniqueConfiguredGPUIDs(ids []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(ids))
	for _, id := range TrimGPUIDList(ids) {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func inventoryByConfiguredID(id string, inventory []GPUInfo) (GPUInfo, bool) {
	for _, gpu := range inventory {
		if strings.EqualFold(id, gpu.Index) || strings.EqualFold(id, gpu.UUID) {
			return gpu, true
		}
	}
	return GPUInfo{}, false
}

func launchGuardedGPU(t model.Task, now time.Time, guard time.Duration) bool {
	if t.Status == model.TaskStatusAdmitted {
		// admitted 表示容器尚未确认 running；无论镜像拉取多久都不并发启动第二个任务。
		return true
	}
	if t.Status != model.TaskStatusRunning || guard <= 0 {
		return false
	}
	started := t.UpdatedAt
	if t.RunningAt != nil {
		started = *t.RunningAt
	}
	if started.IsZero() {
		started = t.CreatedAt
	}
	return !started.IsZero() && now.Sub(started) < guard
}

// AssignOpportunisticNVIDIADeviceID 使用真实空闲显存选择最空闲的合格 GPU。
// 原始空闲显存必须同时覆盖 min_start_free_memory_mb 和 reserve_memory_mb。
func AssignOpportunisticNVIDIADeviceID(
	hr config.DockerHostResources,
	policy config.DockerGPUAdmission,
	occupying []model.Task,
	candidate *model.Task,
	inventory []GPUInfo,
	now time.Time,
) (string, error) {
	if candidate == nil || !TaskWantsGPU(candidate.ResGPU) {
		return "", nil
	}
	configured := uniqueConfiguredGPUIDs(hr.GPUIDs)
	if len(configured) == 0 {
		return "", fmt.Errorf("docker: GPU requested but host_resources.gpu_ids is empty")
	}

	required := int64(policy.MinStartFreeMemoryMB) + int64(policy.ReserveMemoryMB)
	if required <= 0 {
		return "", fmt.Errorf("docker: invalid opportunistic GPU admission threshold")
	}
	guard := time.Duration(policy.LaunchGuardSeconds) * time.Second
	guarded := make(map[string]bool)
	for i := range occupying {
		if !TaskWantsGPU(occupying[i].ResGPU) {
			continue
		}
		id := AssignedNVIDIAGPUID(&occupying[i])
		if id == "" {
			// 旧调度器把所有配置 GPU 都挂进同一个容器，且没有在 Extra 中记录单卡分配。
			// 在这些遗留 admitted/running 任务结束前保护全部允许设备，避免升级瞬间重叠调度。
			for _, configuredID := range configured {
				guarded[configuredID] = true
			}
			continue
		}
		if launchGuardedGPU(occupying[i], now, guard) {
			guarded[id] = true
		}
	}

	var (
		bestID      string
		bestFree    int64 = -1
		maxObserved int64 = -1
		matched           = 0
		guardedN          = 0
	)
	for _, id := range configured {
		gpu, ok := inventoryByConfiguredID(id, inventory)
		if !ok {
			continue
		}
		matched++
		if gpu.FreeMemoryMB > maxObserved {
			maxObserved = gpu.FreeMemoryMB
		}
		if guarded[id] {
			guardedN++
			continue
		}
		if gpu.FreeMemoryMB >= required && gpu.FreeMemoryMB > bestFree {
			bestID = id
			bestFree = gpu.FreeMemoryMB
		}
	}
	if bestID != "" {
		return bestID, nil
	}
	if matched == 0 {
		return "", fmt.Errorf("docker: none of configured gpu_ids found in nvidia-smi inventory")
	}
	return "", fmt.Errorf(
		"docker: insufficient opportunistic GPU memory: required_free_mb=%d (start=%d reserve=%d), max_observed_free_mb=%d, launch_guarded=%d",
		required,
		policy.MinStartFreeMemoryMB,
		policy.ReserveMemoryMB,
		maxObserved,
		guardedN,
	)
}
