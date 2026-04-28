package docker

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-units"
)

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
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	n, err := units.RAMInBytes(s)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// gpuDeviceRequests 从 res_gpu 生成 nvidia 设备请求；逗号分隔，同一 id 重复则多次传入 DeviceIDs（对应多槽绑同一物理设备）。
func gpuDeviceRequests(resGPU string) []container.DeviceRequest {
	s := strings.TrimSpace(resGPU)
	if s == "" {
		return nil
	}
	dr := container.DeviceRequest{
		Driver:       "nvidia",
		Capabilities: [][]string{{"gpu"}},
	}
	if strings.EqualFold(s, "all") {
		dr.DeviceIDs = []string{"all"}
		return []container.DeviceRequest{dr}
	}
	parts := strings.Split(s, ",")
	var ids []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			ids = append(ids, p)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	dr.DeviceIDs = ids
	return []container.DeviceRequest{dr}
}
