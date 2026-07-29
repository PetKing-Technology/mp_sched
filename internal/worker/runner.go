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
	retryDelay := time.Duration(cfg.Worker.NotAdmittedRetryMS) * time.Millisecond
	if retryDelay <= 0 {
		retryDelay = 2 * time.Second
	}
	idleWait := poll
	if retryDelay < idleWait {
		idleWait = retryDelay
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
				time.Sleep(idleWait)
				continue
			}
			slog.Warn("worker claim", "err", err.Error())
			time.Sleep(idleWait)
			continue
		}
		// processing/pending 只在首次准入尝试时回调。后续机会式显存重试仍记录在
		// schedule_attempts/pending_reason，避免每两秒向业务侧重复推送状态抖动。
		notifyAdmissionAttempt := t.ScheduleAttempts == 0
		if notifyAdmissionAttempt && p != nil && p.CB != nil {
			p.CB.Fire(ctx, callback.EventProcessing, t)
		}
		if err := p.Dispatch(ctx, t.TaskID); err != nil {
			if errors.Is(err, taskrepo.ErrNotAdmitted) {
				if deferErr := repo.DeferToPending(t.TaskID, err.Error(), retryDelay); deferErr != nil {
					slog.Error("worker defer pending", "task_id", t.TaskID, "err", deferErr.Error())
					continue
				}
				if notifyAdmissionAttempt && p != nil && p.CB != nil {
					if t2, e := repo.Get(t.TaskID); e == nil {
						p.CB.Fire(ctx, callback.EventPending, t2)
					}
				}
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
