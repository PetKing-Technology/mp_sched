package docker

import (
	"fmt"
	"strings"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
	"mp_sched/internal/provider"
)

// validateTaskHostResources 将 task 的 res_cpu/res_memory 与 [docker.host_resources] 本机上限比对。
// GPU 槽位（gpu_ids 重复项、多任务占用）在 pipeline 中通过 CheckDockerGPUOccupancy 校验。
// 若 max_cpu、max_memory 均未配置，则不校验，直接 nil。
func validateTaskHostResources(t *model.Task, cfg *config.Docker) *provider.ResourceCheckResult {
	if t == nil || cfg == nil {
		return nil
	}
	hr := cfg.HostResources
	hasMaxCPU := strings.TrimSpace(hr.MaxCPU) != ""
	hasMaxMem := strings.TrimSpace(hr.MaxMemory) != ""
	if !hasMaxCPU && !hasMaxMem {
		return nil
	}
	if hasMaxCPU {
		maxNano, err := parseNanoCPUs(hr.MaxCPU)
		if err != nil {
			return &provider.ResourceCheckResult{OK: false, Reason: "docker: invalid host_resources.max_cpu: " + err.Error()}
		}
		if maxNano > 0 {
			reqNano, err := parseNanoCPUs(t.ResCPU)
			if err != nil {
				return &provider.ResourceCheckResult{OK: false, Reason: "docker: res_cpu: " + err.Error()}
			}
			if reqNano > maxNano {
				return &provider.ResourceCheckResult{OK: false, Reason: fmt.Sprintf("docker: res_cpu exceeds host max (%s)", hr.MaxCPU)}
			}
		}
	}
	if hasMaxMem {
		maxB, err := parseMemoryBytes(hr.MaxMemory)
		if err != nil {
			return &provider.ResourceCheckResult{OK: false, Reason: "docker: invalid host_resources.max_memory: " + err.Error()}
		}
		if maxB > 0 {
			reqB, err := parseMemoryBytes(t.ResMemory)
			if err != nil {
				return &provider.ResourceCheckResult{OK: false, Reason: "docker: res_memory: " + err.Error()}
			}
			if reqB > maxB {
				return &provider.ResourceCheckResult{OK: false, Reason: fmt.Sprintf("docker: res_memory exceeds host max (%s)", hr.MaxMemory)}
			}
		}
	}
	return nil
}
