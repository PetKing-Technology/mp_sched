package worker

import (
	"context"
	"log/slog"
	"time"

	"mp_sched/internal/callback"
	"mp_sched/internal/config"
	"mp_sched/internal/model"
	"mp_sched/internal/pipeline"
	"mp_sched/internal/taskrepo"
	"mp_sched/internal/telemetry"
)

// StartRuntimeTimeoutSweeper 周期性扫描 running 任务，超过 max_runtime 则 Stop、标 failed、callback.EventTimeout
func StartRuntimeTimeoutSweeper(ctx context.Context, cfg *config.App, pl *pipeline.Pipeline, repo *taskrepo.Repo) {
	if cfg == nil || pl == nil || repo == nil || cfg.Worker.DisableRuntimeSweeper {
		return
	}
	intv := time.Duration(cfg.Worker.RuntimeSweeperIntervalSeconds) * time.Second
	if intv <= 0 {
		return
	}
	go runRuntimeTimeoutLoop(ctx, cfg, pl, repo, intv)
}

func runRuntimeTimeoutLoop(ctx context.Context, cfg *config.App, pl *pipeline.Pipeline, repo *taskrepo.Repo, intv time.Duration) {
	t := time.NewTicker(intv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweepRuntimeTimeouts(ctx, cfg, pl, repo)
		}
	}
}

func effectiveMaxRuntimeSec(task *model.Task, defaultSec int) int {
	if task == nil {
		return defaultSec
	}
	if task.MaxRuntimeSeconds < 0 {
		return -1
	}
	if task.MaxRuntimeSeconds > 0 {
		return task.MaxRuntimeSeconds
	}
	return defaultSec
}

func sweepRuntimeTimeouts(ctx context.Context, cfg *config.App, pl *pipeline.Pipeline, repo *taskrepo.Repo) {
	tasks, err := repo.ListRunningStartWorkloadsWithRef()
	if err != nil {
		slog.Warn("runtime sweeper list", "err", err.Error())
		return
	}
	now := time.Now()
	def := cfg.Worker.DefaultMaxRuntimeSeconds
	for i := range tasks {
		t := &tasks[i]
		limit := effectiveMaxRuntimeSec(t, def)
		if limit <= 0 {
			continue
		}
		start := t.RunningAt
		if start == nil {
			start = &t.UpdatedAt
		}
		if now.Sub(*start) < time.Duration(limit)*time.Second {
			continue
		}
		slog.Info("runtime timeout: stop workload", "task_id", t.TaskID, "limit_sec", limit)
		prov, err := pl.Reg.Get(t.Provider)
		if err != nil {
			slog.Warn("runtime sweeper provider", "task_id", t.TaskID, "err", err.Error())
			continue
		}
		if err := prov.Stop(ctx, t); err != nil {
			slog.Warn("runtime sweeper stop", "task_id", t.TaskID, "err", err.Error())
		}
		telemetry.FinalFlushDockerLogs(ctx, cfg, pl.DockerEng, t)
		_ = repo.ClearRuntimeRef(t.TaskID)
		if err := repo.UpdateStatus(t.TaskID, model.TaskStatusFailed); err != nil {
			slog.Warn("runtime sweeper status", "task_id", t.TaskID, "err", err.Error())
			continue
		}
		if pl.CB != nil {
			if t2, e := repo.Get(t.TaskID); e == nil {
				pl.CB.Fire(ctx, callback.EventTimeout, t2)
			}
		}
	}
}
