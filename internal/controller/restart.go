package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"mp_sched/internal/model"
	"mp_sched/internal/pipeline"
)

// ErrRestartConflict 任务仍在排队或处理中，无法重启。
var ErrRestartConflict = errors.New("task is pending or processing, cannot restart")

// RestartStartWorkload 对「start 类」任务：终态则克隆为新 pending；running/admitted 则先入队 stop 再克隆新 start（需 worker 按序处理，多 worker 时有竞态可能，生产建议单 worker 或先停后启）。
func (h *Handlers) RestartStartWorkload(ctx context.Context, taskID string) (stopTask *model.Task, newTask *model.Task, err error) {
	if h == nil || h.Repo == nil || h.Pl == nil {
		return nil, nil, errors.New("not configured")
	}
	t, err := h.Repo.Get(taskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, err
		}
		return nil, nil, err
	}
	if isStopOp(t) {
		return nil, nil, errors.New("cannot restart: task is a stop request")
	}
	if t.Status == model.TaskStatusPending || t.Status == model.TaskStatusProcessing {
		return nil, nil, fmt.Errorf("%w (status=%s)", ErrRestartConflict, t.Status)
	}
	in := cloneSubmitInput(t)
	if t.Status == model.TaskStatusRunning || t.Status == model.TaskStatusAdmitted {
		st, e := h.EnqueueStopForTarget(ctx, t.TaskID)
		if e != nil {
			return nil, nil, fmt.Errorf("enqueue stop: %w", e)
		}
		nt, e := h.Pl.Submit(ctx, in)
		if e != nil {
			return st, nil, fmt.Errorf("enqueue new start: %w", e)
		}
		slog.Info("controller restart", "orig_task_id", taskID, "stop_task_id", st.TaskID, "new_task_id", nt.TaskID)
		return st, nt, nil
	}
	// 终态：succeeded, failed, stopped
	if t.Status == model.TaskStatusSucceeded || t.Status == model.TaskStatusFailed || t.Status == model.TaskStatusStopped {
		nt, e := h.Pl.Submit(ctx, in)
		if e != nil {
			return nil, nil, e
		}
		slog.Info("controller restart", "orig_task_id", taskID, "new_task_id", nt.TaskID)
		return nil, nt, nil
	}
	return nil, nil, fmt.Errorf("restart not defined for status %q", t.Status)
}

func isStopOp(t *model.Task) bool {
	if t == nil {
		return true
	}
	if t.Operation == model.OperationStop {
		return true
	}
	return false
}

func cloneSubmitInput(t *model.Task) pipeline.SubmitInput {
	bz := []byte(t.Business)
	if len(bz) == 0 {
		bz = []byte(`{}`)
	}
	return pipeline.SubmitInput{
		Business:          bz,
		TaskClass:         t.TaskClass,
		ProviderName:      t.Provider,
		Operation:         model.OperationStart,
		TargetTaskID:      "",
		Image:             t.Image,
		ResCPU:            t.ResCPU,
		ResMemory:         t.ResMemory,
		ResGPU:            t.ResGPU,
		MaxRuntimeSeconds: t.MaxRuntimeSeconds,
	}
}
