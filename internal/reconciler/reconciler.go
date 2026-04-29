package reconciler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	dockercli "github.com/docker/docker/client"

	"mp_sched/internal/callback"
	"mp_sched/internal/config"
	"mp_sched/internal/model"
	"mp_sched/internal/provider"
	"mp_sched/internal/taskrepo"
	"mp_sched/internal/telemetry"
)

// Runner 周期性用 Provider.Status 对账，把终态从集群同步回库；可选对「终态+残留 runtime」做孤儿回收
type Runner struct {
	Repo *taskrepo.Repo
	Reg  provider.Registry
	CB   *callback.Client
	Cfg  *config.Reconciler
	App  *config.App
	Eng  *dockercli.Client

	orphanInFlight sync.Map
}

// Tick 对 admitted/running 的 start 工作负载做一轮拉取
func (r *Runner) Tick(ctx context.Context) {
	if r == nil || r.Repo == nil {
		return
	}
	ta, err := r.Repo.ListStartByStatuses(taskrepo.SlotStatuses)
	if err != nil {
		slog.Warn("reconciler list", "err", err.Error())
		return
	}
	for i := range ta {
		t := &ta[i]
		prov, err := r.Reg.Get(t.Provider)
		if err != nil {
			slog.Warn("reconciler provider", "provider", t.Provider, "err", err.Error())
			continue
		}
		st, err := prov.Status(ctx, t)
		if err != nil {
			slog.Warn("reconciler status", "task_id", t.TaskID, "err", err.Error())
			continue
		}
		if st == nil {
			continue
		}
		var ev string
		switch st.Phase {
		case provider.PhaseSucceeded:
			ev = callback.EventSucceeded
			telemetry.FinalFlushDockerLogs(ctx, r.App, r.Eng, t)
			_ = r.Repo.UpdateStatus(t.TaskID, model.TaskStatusSucceeded)
		case provider.PhaseFailed:
			ev = callback.EventFailed
			telemetry.FinalFlushDockerLogs(ctx, r.App, r.Eng, t)
			_ = r.Repo.UpdateStatus(t.TaskID, model.TaskStatusFailed)
		case provider.PhaseStopped:
			ev = callback.EventStopped
			telemetry.FinalFlushDockerLogs(ctx, r.App, r.Eng, t)
			_ = r.Repo.UpdateStatus(t.TaskID, model.TaskStatusStopped)
		default:
		}
		if ev != "" && r.CB != nil {
			if t2, e := r.Repo.Get(t.TaskID); e == nil {
				r.CB.Fire(ctx, ev, t2)
			}
		}
	}
}

// TickOrphanReap 扫描库中已终态但仍有 runtime_ref 的 docker 任务；对仍在跑的容器按间隔复测，最后仍不退出则 Stop 并清 ref
func (r *Runner) TickOrphanReap(ctx context.Context) {
	if r == nil || r.Repo == nil || r.Cfg == nil || !r.Cfg.OrphanReapEnable {
		return
	}
	tasks, err := r.Repo.ListTerminalDockerWithRuntimeRef()
	if err != nil {
		slog.Warn("reconciler orphan list", "err", err.Error())
		return
	}
	for i := range tasks {
		tid := tasks[i].TaskID
		if _, loaded := r.orphanInFlight.LoadOrStore(tid, true); loaded {
			continue
		}
		go r.reapOrphanGoroutine(ctx, tid)
	}
}

func (r *Runner) orphanReapWaits() []int {
	if r == nil || r.Cfg == nil || len(r.Cfg.OrphanReapWaitsS) == 0 {
		return []int{5, 15, 30}
	}
	return r.Cfg.OrphanReapWaitsS
}

func (r *Runner) reapOrphanGoroutine(ctx context.Context, taskID string) {
	defer r.orphanInFlight.Delete(taskID)
	t, err := r.Repo.Get(taskID)
	if err != nil {
		return
	}
	if t.Provider != "docker" {
		return
	}
	prov, err := r.Reg.Get(t.Provider)
	if err != nil {
		slog.Warn("orphan provider", "task_id", taskID, "err", err.Error())
		return
	}
	st, err := prov.Status(ctx, t)
	if err != nil {
		slog.Warn("orphan status", "task_id", taskID, "err", err.Error())
		return
	}
	if st == nil {
		return
	}
	if st.Phase == provider.PhaseRunning {
		for _, sec := range r.orphanReapWaits() {
			if err := sleepCtx(ctx, time.Duration(sec)*time.Second); err != nil {
				return
			}
			t2, err := r.Repo.Get(taskID)
			if err != nil {
				return
			}
			st2, err := prov.Status(ctx, t2)
			if err != nil {
				slog.Warn("orphan status", "task_id", taskID, "err", err.Error())
				return
			}
			if st2 == nil {
				return
			}
			if st2.Phase != provider.PhaseRunning {
				if workloadGoneForClear(st2.Phase) {
					r.clearStaleRuntimeRef(t2, st2)
				}
				return
			}
		}
		slog.Info("orphan: workload still running after watches, stop", "task_id", t.TaskID, "runtime_ref", t.RuntimeRef)
		t3, err := r.Repo.Get(taskID)
		if err != nil {
			return
		}
		if err := prov.Stop(ctx, t3); err != nil {
			slog.Warn("orphan stop", "task_id", t3.TaskID, "err", err.Error())
		}
		telemetry.FinalFlushDockerLogs(ctx, r.App, r.Eng, t3)
		if err := r.Repo.ClearRuntimeRef(t3.TaskID); err != nil {
			slog.Warn("orphan clear runtime_ref", "task_id", t3.TaskID, "err", err.Error())
		} else {
			slog.Info("orphan: stopped and cleared runtime_ref", "task_id", t3.TaskID, "db_status", t3.Status)
		}
		return
	}
	if workloadGoneForClear(st.Phase) {
		r.clearStaleRuntimeRef(t, st)
	}
}

func workloadGoneForClear(phase string) bool {
	switch phase {
	case provider.PhaseSucceeded, provider.PhaseFailed, provider.PhaseStopped:
		return true
	default:
		return false
	}
}

func (r *Runner) clearStaleRuntimeRef(t *model.Task, st *provider.RuntimeStatus) {
	if t == nil || st == nil || r.Repo == nil {
		return
	}
	if err := r.Repo.ClearRuntimeRef(t.TaskID); err != nil {
		slog.Warn("orphan clear runtime_ref", "task_id", t.TaskID, "err", err.Error())
	} else {
		slog.Info("orphan: cleared stale runtime_ref", "task_id", t.TaskID, "db_status", t.Status, "runtime_phase", st.Phase, "msg", st.Message)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// RunLoop 阻塞在 interval 上循环 Tick，ctx 取消即退出
func (r *Runner) RunLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if r.Cfg != nil && r.Cfg.Enable {
				r.Tick(ctx)
			}
			if r.Cfg != nil && r.Cfg.OrphanReapEnable {
				r.TickOrphanReap(ctx)
			}
		}
	}
}
