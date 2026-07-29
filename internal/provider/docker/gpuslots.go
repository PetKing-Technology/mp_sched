package docker

import (
	"encoding/json"
	"fmt"
	"strings"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

const extraDockerGPUIDKey = "docker_gpu_id"

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

// ResolveNVIDIADeviceIDsForAttach 返回 host_resources.gpu_ids 清理后的槽位列表，保留重复项。
// 每个元素是一条可分配槽位；同一个 device id 出现两次，表示它可以分给两个容器。
func ResolveNVIDIADeviceIDsForAttach(hr *config.DockerHostResources) ([]string, error) {
	if hr == nil {
		return nil, nil
	}
	return TrimGPUIDList(hr.GPUIDs), nil
}

// AssignedNVIDIAGPUID 返回 admit 阶段写入 task.Extra 的 Docker GPU device id。
func AssignedNVIDIAGPUID(t *model.Task) string {
	if t == nil || len(t.Extra) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(t.Extra, &m); err != nil {
		return ""
	}
	v, _ := m[extraDockerGPUIDKey].(string)
	return strings.TrimSpace(v)
}

// ExtraWithAssignedNVIDIAGPUID 在 Extra JSON 中写入本次分配到的 Docker GPU device id。
func ExtraWithAssignedNVIDIAGPUID(raw []byte, id string) ([]byte, error) {
	m := map[string]any{}
	if len(raw) > 0 && strings.TrimSpace(string(raw)) != "" {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("docker: parse task extra: %w", err)
		}
	}
	id = strings.TrimSpace(id)
	if id == "" {
		delete(m, extraDockerGPUIDKey)
	} else {
		m[extraDockerGPUIDKey] = id
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("docker: encode task extra: %w", err)
	}
	return out, nil
}

func removeOneGPUSlot(slots []string, id string) ([]string, bool) {
	id = strings.TrimSpace(id)
	for i, slot := range slots {
		if slot == id {
			copy(slots[i:], slots[i+1:])
			return slots[:len(slots)-1], true
		}
	}
	return slots, false
}

// AvailableNVIDIADeviceIDs 从 host_resources.gpu_ids 的槽位列表中扣掉 admitted/running 任务已占用的 id。
// 对旧数据若任务需要 GPU 但 Extra 中还没有 docker_gpu_id，则按当前剩余列表的第一个槽位保守扣减。
func AvailableNVIDIADeviceIDs(hr config.DockerHostResources, occupying []model.Task) ([]string, error) {
	available := TrimGPUIDList(hr.GPUIDs)
	for i := range occupying {
		if !TaskWantsGPU(occupying[i].ResGPU) {
			continue
		}
		id := AssignedNVIDIAGPUID(&occupying[i])
		if id == "" {
			if len(available) == 0 {
				return nil, fmt.Errorf("docker: gpu slots exhausted by existing tasks")
			}
			id = available[0]
		}
		var ok bool
		available, ok = removeOneGPUSlot(available, id)
		if !ok {
			return nil, fmt.Errorf("docker: gpu slots exhausted for %q (task_id=%s)", id, occupying[i].TaskID)
		}
	}
	return available, nil
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

// AssignNVIDIADeviceIDForTask 为当前任务从剩余 GPU 槽位中取第一个 device id。
func AssignNVIDIADeviceIDForTask(hr config.DockerHostResources, occupying []model.Task, candidate *model.Task) (string, error) {
	if candidate == nil || !TaskWantsGPU(candidate.ResGPU) {
		return "", nil
	}
	if len(TrimGPUIDList(hr.GPUIDs)) == 0 {
		return "", fmt.Errorf("docker: GPU requested but host_resources.gpu_ids is empty")
	}
	available, err := AvailableNVIDIADeviceIDs(hr, occupying)
	if err != nil {
		return "", err
	}
	if len(available) == 0 {
		return "", fmt.Errorf("docker: no available GPU device id for task_id=%s", candidate.TaskID)
	}
	return available[0], nil
}

// CheckDockerGPUOccupancy 对当前已列出的 docker admitted/running start 任务做 GPU 槽位汇总校验。
func CheckDockerGPUOccupancy(hr config.DockerHostResources, tasks []model.Task) error {
	if len(TrimGPUIDList(hr.GPUIDs)) == 0 {
		for i := range tasks {
			if TaskWantsGPU(tasks[i].ResGPU) {
				return fmt.Errorf("docker: GPU requested (task_id=%s) but host_resources.gpu_ids is empty", tasks[i].TaskID)
			}
		}
		return nil
	}
	_, err := AvailableNVIDIADeviceIDs(hr, tasks)
	return err
}

// CheckDockerGPUOccupancyWithCandidate 在 occupying 之上再计入若 candidate 被 admit 后的 GPU 占用（candidate 通常为尚处于 processing 的本人）。
func CheckDockerGPUOccupancyWithCandidate(hr config.DockerHostResources, occupying []model.Task, candidate *model.Task) error {
	if candidate == nil {
		return CheckDockerGPUOccupancy(hr, occupying)
	}
	_, err := AssignNVIDIADeviceIDForTask(hr, occupying, candidate)
	return err
}
