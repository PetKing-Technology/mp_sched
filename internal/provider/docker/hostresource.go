package docker

import (
	"fmt"
	"strings"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
	"mp_sched/internal/provider"
)

// validateTaskHostResources 将 task 的 res_cpu/res_memory 与 [docker.host_resources] 本机上限比对；
// 若任务需要 GPU，校验 gpu_ids 去重后能解析出合法挂载且 id 均在槽位表中。
// GPU 多任务并发占用由 pipeline 在 admit 事务内 CheckDockerGPUOccupancyWithCandidate 汇总校验。
// 若 max_cpu、max_memory 均未配置且无 GPU 请求，且不触发 GPU 校验，则可能直接 nil。
func validateTaskHostResources(t *model.Task, cfg *config.Docker) *provider.ResourceCheckResult {
	if t == nil || cfg == nil {
		return nil
	}
	hr := cfg.HostResources
	hasMaxCPU := strings.TrimSpace(hr.MaxCPU) != ""
	hasMaxMem := strings.TrimSpace(hr.MaxMemory) != ""
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
	if TaskWantsGPU(t.ResGPU) {
		hostCap := HostGPUSlotCapacity(hr.GPUIDs)
		if len(hostCap) == 0 {
			return &provider.ResourceCheckResult{OK: false, Reason: "docker: GPU requested but host_resources.gpu_ids is empty"}
		}
		attach, err := ResolveNVIDIADeviceIDsForAttach(&hr)
		if err != nil {
			return &provider.ResourceCheckResult{OK: false, Reason: err.Error()}
		}
		if _, err := gpuSlotNeedFromAttach(hostCap, attach); err != nil {
			return &provider.ResourceCheckResult{OK: false, Reason: err.Error()}
		}
	}
	return nil
}
