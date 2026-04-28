package docker

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"mp_sched/internal/config"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-units"
)

// kubernetesBinaryRAM 匹配 K8s 资源写法 Mi/Gi/Ti…（无尾字母 B）；docker/go-units 的 RAMInBytes 需要 gb / mib / gib 等形式。
var kubernetesBinaryRAM = regexp.MustCompile(`(?i)^([0-9]*\.?[0-9]+)([kmgtpe])i$`)

func normalizeMemoryStringForRAM(s string) string {
	s = strings.TrimSpace(s)
	if m := kubernetesBinaryRAM.FindStringSubmatch(s); m != nil {
		return m[1] + strings.ToLower(m[2]) + "ib"
	}
	return s
}

// parseNanoCPUs 解析 res_cpu：空=0（不限制）；支持 "1"/"0.5"/"500m"（毫核）
func parseNanoCPUs(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if strings.HasSuffix(s, "m") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "m"), 64)
		if err != nil {
			return 0, fmt.Errorf("cpu millicores: %w", err)
		}
		// 1000m = 1 CPU = 1e9 NanoCPUs
		return int64(v * 1e6), nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("cpu: %w", err)
	}
	return int64(v * 1e9), nil
}

func parseMemoryBytes(s string) (int64, error) {
	s = normalizeMemoryStringForRAM(s)
	if s == "" {
		return 0, nil
	}
	n, err := units.RAMInBytes(s)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// gpuDeviceRequestsForTask 在任务需要 GPU 时用 host_resources 解析出的 id 构造 NVIDIA DeviceRequest。
func gpuDeviceRequestsForTask(resGPU string, hr *config.DockerHostResources) []container.DeviceRequest {
	if !TaskWantsGPU(resGPU) {
		return nil
	}
	ids, err := ResolveNVIDIADeviceIDsForAttach(hr)
	if err != nil || len(ids) == 0 {
		return nil
	}
	dr := container.DeviceRequest{
		Driver:       "nvidia",
		Capabilities: [][]string{{"gpu"}},
		DeviceIDs:    ids,
	}
	return []container.DeviceRequest{dr}
}
