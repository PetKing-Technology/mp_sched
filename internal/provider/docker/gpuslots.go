package docker

import (
	"fmt"
	"strings"

	"mp_sched/internal/model"
)

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

// TaskGPUSlotNeed 单任务 res_gpu 占用的槽位；逗号分隔，同一 id 出现多次算多次。
// res_gpu 为 all 时占满配置中的全部槽位（与 hostCap 一致）。hostCap 为空且 res 非空返回错误。
func TaskGPUSlotNeed(resGPU string, hostCap map[string]int) (map[string]int, error) {
	s := strings.TrimSpace(resGPU)
	if s == "" {
		return nil, nil
	}
	if len(hostCap) == 0 {
		return nil, fmt.Errorf("docker: res_gpu is set but host_resources.gpu_ids is empty")
	}
	if strings.EqualFold(s, "all") {
		out := make(map[string]int, len(hostCap))
		for k, v := range hostCap {
			out[k] = v
		}
		return out, nil
	}
	out := make(map[string]int)
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := hostCap[p]; !ok {
			return nil, fmt.Errorf("docker: res_gpu id %q not in host_resources.gpu_ids", p)
		}
		out[p]++
	}
	return out, nil
}

// SumTaskGPUNeeds 将多行任务的 res_gpu 槽位需求按 id 相加。
func SumTaskGPUNeeds(tasks []model.Task, hostCap map[string]int) (map[string]int, error) {
	total := make(map[string]int)
	for i := range tasks {
		m, err := TaskGPUSlotNeed(tasks[i].ResGPU, hostCap)
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

// CheckDockerGPUOccupancy 对当前已列出的 docker admitted/running start 任务做 GPU 槽位汇总校验（含 res_gpu 重复计数）。
func CheckDockerGPUOccupancy(hostIDs []string, tasks []model.Task) error {
	hostCap := HostGPUSlotCapacity(hostIDs)
	if len(hostCap) == 0 {
		return nil
	}
	used, err := SumTaskGPUNeeds(tasks, hostCap)
	if err != nil {
		return err
	}
	return ValidateGPUUsedWithinHostCapacity(hostCap, used)
}
