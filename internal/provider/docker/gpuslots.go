package docker

import (
	"encoding/json"
	"fmt"
	"strings"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

const extraDockerGPUIDKey = "docker_gpu_id"

func AssignedNVIDIAGPUID(task *model.Task) string {
	if task == nil || len(task.Extra) == 0 { return "" }
	var values map[string]any
	if err := json.Unmarshal(task.Extra, &values); err != nil { return "" }
	value, _ := values[extraDockerGPUIDKey].(string)
	return strings.TrimSpace(value)
}

func ExtraWithAssignedNVIDIAGPUID(raw []byte, id string) ([]byte, error) {
	values := map[string]any{}
	if len(raw) > 0 && strings.TrimSpace(string(raw)) != "" {
		if err := json.Unmarshal(raw, &values); err != nil { return nil, fmt.Errorf("docker: parse task extra: %w", err) }
	}
	if id = strings.TrimSpace(id); id == "" { delete(values, extraDockerGPUIDKey) } else { values[extraDockerGPUIDKey] = id }
	encoded, err := json.Marshal(values)
	if err != nil { return nil, fmt.Errorf("docker: encode task extra: %w", err) }
	return encoded, nil
}

func AssignNVIDIADeviceIDForTask(host config.DockerHostResources, occupying []model.Task, candidate *model.Task) (string, error) {
	if candidate == nil || !TaskWantsGPU(candidate.ResGPU) { return "", nil }
	slots := TrimGPUIDList(host.GPUIDs)
	if len(slots) == 0 { return "", fmt.Errorf("docker: GPU requested but host_resources.gpu_ids is empty") }
	for i := range occupying {
		if !TaskWantsGPU(occupying[i].ResGPU) { continue }
		id := AssignedNVIDIAGPUID(&occupying[i])
		if id == "" { id = slots[0] }
		found := -1
		for j, slot := range slots { if slot == id { found = j; break } }
		if found < 0 { return "", fmt.Errorf("docker: gpu slots exhausted for %q (task_id=%s)", id, occupying[i].TaskID) }
		slots = append(slots[:found], slots[found+1:]...)
	}
	if len(slots) == 0 { return "", fmt.Errorf("docker: no available GPU device id for task_id=%s", candidate.TaskID) }
	return slots[0], nil
}

// TrimGPUIDList 从配置读取的 gpu_ids 去空白、丢弃空串，保留重复项（每一项对应一个可分配槽位）。
func TrimGPUIDList(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

// HostGPUSlotCapacity 由 host_resources.gpu_ids 得到各 device id 的槽位数（重复即多槽）。
func HostGPUSlotCapacity(hostIDs []string) map[string]int {
	list := TrimGPUIDList(hostIDs)
	if len(list) == 0 {
		return nil
	}
	m := make(map[string]int)
	for _, id := range list {
		m[id]++
	}
	return m
}

// TaskWantsGPU 从任务 JSON 的 res_gpu 判断是否需要挂载 GPU。
// 仅 "1"、"true"、"yes"、"on"（不区分大小写）为开；其余含空串、历史字段 GPU-0/all 均为关，避免未配 gpu_ids 的 worker 被旧任务拖死。
func TaskWantsGPU(resGPU string) bool {
	s := strings.ToLower(strings.TrimSpace(resGPU))
	switch s {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// ResolveNVIDIADeviceIDsForAttach 从 host_resources.gpu_ids 按首次出现顺序去重，得到写入 NVIDIA DeviceRequests 的 id 列表（槽位仍由 gpu_ids 多重集单独统计）。
func ResolveNVIDIADeviceIDsForAttach(hr *config.DockerHostResources) ([]string, error) {
	if hr == nil {
		return nil, nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, id := range TrimGPUIDList(hr.GPUIDs) {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func gpuSlotNeedFromAttach(hostCap map[string]int, attach []string) (map[string]int, error) {
	out := make(map[string]int)
	for _, id := range attach {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := hostCap[id]; !ok {
			return nil, fmt.Errorf("docker: nvidia device id %q not in host_resources.gpu_ids", id)
		}
		out[id]++
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("docker: no nvidia device ids resolved for GPU task")
	}
	return out, nil
}

// TaskGPUSlotNeed 单任务在需要 GPU 时占用的槽位：对 gpu_ids 去重后的每个 device id 各计 1 槽（同一任务不会在单一 id 上因重复项多占槽；多卡则每卡各计 1）。
func TaskGPUSlotNeed(resGPU string, hostCap map[string]int, hr *config.DockerHostResources) (map[string]int, error) {
	if !TaskWantsGPU(resGPU) {
		return nil, nil
	}
	if len(hostCap) == 0 {
		return nil, fmt.Errorf("docker: GPU requested but host_resources.gpu_ids is empty")
	}
	attach, err := ResolveNVIDIADeviceIDsForAttach(hr)
	if err != nil {
		return nil, err
	}
	return gpuSlotNeedFromAttach(hostCap, attach)
}

// SumTaskGPUNeeds 将多行任务的 GPU 槽位需求按 id 相加。
func SumTaskGPUNeeds(tasks []model.Task, hostCap map[string]int, hr *config.DockerHostResources) (map[string]int, error) {
	total := make(map[string]int)
	for i := range tasks {
		m, err := TaskGPUSlotNeed(tasks[i].ResGPU, hostCap, hr)
		if err != nil {
			return nil, fmt.Errorf("%w (task_id=%s)", err, tasks[i].TaskID)
		}
		for k, v := range m {
			total[k] += v
		}
	}
	return total, nil
}

// ValidateGPUUsedWithinHostCapacity 校验各 id 总占用不超过 host 槽位。
func ValidateGPUUsedWithinHostCapacity(hostCap, used map[string]int) error {
	for id, n := range used {
		capN, ok := hostCap[id]
		if !ok {
			return fmt.Errorf("docker: gpu slot accounting: unknown id %q", id)
		}
		if n > capN {
			return fmt.Errorf("docker: gpu slots exhausted for %q (in use %d, host has %d slots)", id, n, capN)
		}
	}
	return nil
}

// RemainingGPUSlots 返回各 id 剩余槽位（仅 hostCap 中出现的 id；已用超过容量时为 0）。
func RemainingGPUSlots(hostCap, used map[string]int) map[string]int {
	rem := make(map[string]int, len(hostCap))
	for id, capN := range hostCap {
		u := used[id]
		if capN > u {
			rem[id] = capN - u
		} else {
			rem[id] = 0
		}
	}
	return rem
}

// CheckDockerGPUOccupancy 对当前已列出的 docker admitted/running start 任务做 GPU 槽位汇总校验。
func CheckDockerGPUOccupancy(hr config.DockerHostResources, tasks []model.Task) error {
	hostCap := HostGPUSlotCapacity(hr.GPUIDs)
	if len(hostCap) == 0 {
		for i := range tasks {
			if TaskWantsGPU(tasks[i].ResGPU) {
				return fmt.Errorf("docker: GPU requested (task_id=%s) but host_resources.gpu_ids is empty", tasks[i].TaskID)
			}
		}
		return nil
	}
	used, err := SumTaskGPUNeeds(tasks, hostCap, &hr)
	if err != nil {
		return err
	}
	return ValidateGPUUsedWithinHostCapacity(hostCap, used)
}

// CheckDockerGPUOccupancyWithCandidate 在 occupying 之上再计入若 candidate 被 admit 后的 GPU 占用（candidate 通常为尚处于 processing 的本人）。
func CheckDockerGPUOccupancyWithCandidate(hr config.DockerHostResources, occupying []model.Task, candidate *model.Task) error {
	if candidate == nil {
		return CheckDockerGPUOccupancy(hr, occupying)
	}
	combined := make([]model.Task, len(occupying)+1)
	copy(combined, occupying)
	combined[len(occupying)] = *candidate
	return CheckDockerGPUOccupancy(hr, combined)
}
