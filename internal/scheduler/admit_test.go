package scheduler

import (
	"testing"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

func TestAdmit_SlowReservesFreeSlots(t *testing.T) {
	cfg := config.Scheduler{
		MaxConcurrentRunning: 5,
		MaxConcurrentSlow:    2,
		MinFreeSlotsForFast:  2,
	}
	// 已有 2 个槽位，再接纳 1 个后 total=3, free=2，刚好等于 min_free=2
	next := &model.Task{TaskClass: model.TaskClassSlow, Operation: model.OperationStart}
	if ok, _ := Admit(cfg, next, &RunningCounts{Total: 2, Slow: 0, Fast: 0}); !ok {
		t.Fatal("expected admit")
	}
	// 已有 2 个槽位，再接纳 1 个后 total=3, free=2 -> ok; 但已有 slow=2 时不能再接 slow
	if ok, _ := Admit(cfg, next, &RunningCounts{Total: 2, Slow: 2, Fast: 0}); ok {
		t.Fatal("expected reject slow limit")
	}
	// total=3 时再接一个 slow: after=4, free=1 < min_free=2
	nextSlow := &model.Task{TaskClass: model.TaskClassSlow, Operation: model.OperationStart}
	if ok, _ := Admit(cfg, nextSlow, &RunningCounts{Total: 3, Slow: 0, Fast: 0}); ok {
		t.Fatal("expected reject fast slots reserved")
	}
}

func TestAdmit_FastOnlyTotalCap(t *testing.T) {
	cfg := config.Scheduler{MaxConcurrentRunning: 1, MaxConcurrentSlow: 1, MinFreeSlotsForFast: 0}
	f := &model.Task{TaskClass: model.TaskClassFast, Operation: model.OperationStart}
	if ok, _ := Admit(cfg, f, &RunningCounts{Total: 0, Fast: 0, Slow: 0}); !ok {
		t.Fatal("expected admit")
	}
	if ok, _ := Admit(cfg, f, &RunningCounts{Total: 1, Fast: 1, Slow: 0}); ok {
		t.Fatal("expected global cap")
	}
}

func TestAdmit_StopAlwaysPassesPolicy(t *testing.T) {
	cfg := config.Scheduler{MaxConcurrentRunning: 0, MaxConcurrentSlow: 0, MinFreeSlotsForFast: 0}
	st := &model.Task{Operation: model.OperationStop}
	if ok, _ := Admit(cfg, st, &RunningCounts{Total: 999, Fast: 999, Slow: 999}); !ok {
		t.Fatal("stop 不应被并发槽位挡住")
	}
}
