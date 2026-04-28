package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"mp_sched/internal/callback"
	"mp_sched/internal/config"
	"mp_sched/internal/pipeline"
	"mp_sched/internal/taskrepo"
)

// Run 从库中抢占任务并执行 Dispatch
func Run(ctx context.Context, cfg *config.App, p *pipeline.Pipeline, repo *taskrepo.Repo) {
	if cfg == nil {
		return
	}
	poll := time.Duration(cfg.Worker.PollMS) * time.Millisecond
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	if cfg.Worker.AdmittedTimeoutSeconds > 0 {
		d := time.Duration(cfg.Worker.AdmittedTimeoutSeconds) * time.Second
		go runAdmittedSweeper(ctx, repo, d, poll*5)
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		allowed := cfg.Worker.AllowedProviders
		t, err := repo.ClaimNext(ctx, allowed)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				time.Sleep(poll)
				continue
			}
			slog.Warn("worker claim", "err", err.Error())
			time.Sleep(poll)
			continue
		}
		if p != nil && p.CB != nil {
			p.CB.Fire(ctx, callback.EventProcessing, t)
		}
		if err := p.Dispatch(ctx, t.TaskID); err != nil {
			if errors.Is(err, taskrepo.ErrNotAdmitted) {
				_ = repo.ResetToPending(t.TaskID)
				if p != nil && p.CB != nil {
					if t2, e := repo.Get(t.TaskID); e == nil {
						p.CB.Fire(ctx, callback.EventPending, t2)
					}
				}
				time.Sleep(50 * time.Millisecond)
				continue
			}
			slog.Warn("worker dispatch", "task_id", t.TaskID, "err", err.Error())
		}
	}
}

func runAdmittedSweeper(ctx context.Context, repo *taskrepo.Repo, timeout, every time.Duration) {
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			cut := time.Now().Add(-timeout)
			n, err := repo.RevertAdmittedStuck(cut)
			if err != nil {
				slog.Warn("worker admitted sweep", "err", err.Error())
				continue
			}
			if n == 0 {
				continue
			}
			slog.Info("worker reverted stuck admitted", "count", n, "timeout", timeout.String())
		}
	}
}
