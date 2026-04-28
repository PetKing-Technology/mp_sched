package scheduler

import (
	"fmt"
	"math"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

// RunningCounts 从数据库或其它存储聚合的当前执行态势，用于准入判断。
type RunningCounts struct {
	// Total 当前状态为 running 或 admitted（尚算占用槽位）的总数，按你们与 DB 的约定一致即可
	Total int
	Fast  int
	Slow  int
}

// Admit 结合配置与运行态，判断是否允许新任务进入「可执行」队列。
// 规则要点：
// 1) 总并发不超过 MaxConcurrentRunning
// 2) 慢任务数不超过 MaxConcurrentSlow
// 3) 为快任务保留空闲槽位：接纳一个慢任务后，剩余空槽位仍须 >= MinFreeSlotsForFast，否则拒绝慢任务，避免慢任务把池子占满
func Admit(
	cfg config.Scheduler,
	next *model.Task,
	counts *RunningCounts,
) (ok bool, reason string) {
	if next == nil || counts == nil {
		return false, "nil task or counts"
	}
	// 停止单条请求：不占用 start 的并发槽，策略上直接通过（可后续加全局限流）
	if next.Operation == model.OperationStop {
		return true, ""
	}
	maxT := cfg.MaxConcurrentRunning
	if maxT <= 0 {
		maxT = 1
	}
	// 全局槽位
	if counts.Total >= maxT {
		return false, "global_concurrent_limit"
	}
	// 仅对慢任务加额外约束；未知 class 按「非 slow」处理（只受总 cap 限制，便于扩展）
	if next.TaskClass == model.TaskClassSlow {
		maxSlow := cfg.MaxConcurrentSlow
		if maxSlow < 0 {
			maxSlow = 0
		}
		if counts.Slow >= maxSlow {
			return false, "slow_concurrent_limit"
		}
		after := counts.Total + 1
		free := maxT - after
		res := cfg.MinFreeSlotsForFast
		if res < 0 {
			res = 0
		}
		if free < res {
			return false, "fast_slots_reserved"
		}
	}
	return true, ""
}

// Describe 便于日志与调试
func Describe(cfg config.Scheduler, counts *RunningCounts) string {
	if counts == nil {
		return "counts=nil"
	}
	return fmt.Sprintf("running total=%d fast=%d slow=%d; limits max=%d max_slow=%d min_free_for_fast=%d",
		counts.Total, counts.Fast, counts.Slow,
		cfg.MaxConcurrentRunning, cfg.MaxConcurrentSlow, cfg.MinFreeSlotsForFast)
}

// SlotsAvailable 总剩余槽位（不区分快慢）
func SlotsAvailable(cfg config.Scheduler, totalRunning int) int {
	maxT := cfg.MaxConcurrentRunning
	if maxT <= 0 {
		return 0
	}
	return int(math.Max(0, float64(maxT-totalRunning)))
}
