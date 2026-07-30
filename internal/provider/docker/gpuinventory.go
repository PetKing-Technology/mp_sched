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

// AssignOpportunisticNVIDIADeviceID 使用真实空闲显存选择最空闲的合格 GPU。
// 机会式模式只依据 nvidia-smi 的当前 free memory；容器挂载、admitted/running
// 状态以及历史单卡分配均不代表实际显存占用，不参与准入判断。
func AssignOpportunisticNVIDIADeviceID(
	hr config.DockerHostResources,
	policy config.DockerGPUAdmission,
	_ []model.Task,
	candidate *model.Task,
	inventory []GPUInfo,
	_ time.Time,
) (string, error) {
	if candidate == nil || !TaskWantsGPU(candidate.ResGPU) {
		return "", nil
	}
	configured := uniqueConfiguredGPUIDs(hr.GPUIDs)
	if len(configured) == 0 {
		return "", fmt.Errorf("docker: GPU requested but host_resources.gpu_ids is empty")
	}

	required := int64(policy.MinStartFreeMemoryMB)
	if required <= 0 {
		return "", fmt.Errorf("docker: invalid opportunistic GPU admission threshold")
	}

	var (
		bestID      string
		bestFree    int64 = -1
		maxObserved int64 = -1
		matched           = 0
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
		"docker: insufficient opportunistic GPU memory: required_free_mb=%d, max_observed_free_mb=%d",
		required,
		maxObserved,
	)
}
