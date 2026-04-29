package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	dockercli "github.com/docker/docker/client"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"mp_sched/internal/callback"
	"mp_sched/internal/config"
	"mp_sched/internal/model"
	"mp_sched/internal/provider"
	"mp_sched/internal/provider/docker"
	"mp_sched/internal/recordrepo"
	"mp_sched/internal/scheduler"
	"mp_sched/internal/taskrepo"
	"mp_sched/internal/telemetry"
)

// Pipeline 调度执行链；由 worker 在任务置为 processing 后调用 Dispatch
type Pipeline struct {
	Cfg  *config.App
	Repo *taskrepo.Repo
	Reg  provider.Registry
	Rec  *recordrepo.Repo
	CB   *callback.Client
	// DockerEng 可选；用于终态前补拉容器日志写入 ClickHouse
	DockerEng *dockercli.Client
}

// SubmitInput 与 controller HTTP 入参一致
type SubmitInput struct {
	Business     []byte
	TaskClass    string
	ProviderName string
	Operation    string
	TargetTaskID string
	Image        string
	ResCPU       string
	ResMemory    string
	ResGPU       string
	// MaxRuntimeSeconds 0=worker 默认；>0 覆盖；<0 不限制
	MaxRuntimeSeconds int
}

// Submit 仅生成本地 task 并置 pending（controller 使用）
func (p *Pipeline) Submit(ctx context.Context, in SubmitInput) (*model.Task, error) {
	if p == nil || p.Cfg == nil || p.Repo == nil {
		return nil, errors.New("pipeline: not initialized")
	}
	op := in.Operation
	if op == "" {
		op = model.OperationStart
	}
	if op == model.OperationStop && in.TargetTaskID == "" {
		return nil, errors.New("pipeline: target_task_id required for stop")
	}
	if in.ProviderName == "" {
		return nil, errors.New("pipeline: empty provider")
	}
	bz := in.Business
	if len(bz) == 0 {
		bz = []byte(`{}`)
	}
	t := &model.Task{
		TaskID:            uuid.NewString(),
		Business:          datatypes.JSON(bz),
		Image:             in.Image,
		TaskClass:         in.TaskClass,
		Provider:          in.ProviderName,
		Operation:         op,
		TargetTaskID:      in.TargetTaskID,
		ResCPU:            in.ResCPU,
		ResMemory:         in.ResMemory,
		ResGPU:            in.ResGPU,
		MaxRuntimeSeconds: in.MaxRuntimeSeconds,
		Extra:             datatypes.JSON(`{}`),
		Status:            model.TaskStatusPending,
	}
	if err := p.Repo.Create(t); err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	if p.CB != nil {
		p.CB.Fire(ctx, callback.EventPending, t)
	}
	return t, nil
}

// Dispatch 仅应在 worker 将任务从 pending 置为 processing 后调用
func (p *Pipeline) Dispatch(ctx context.Context, taskID string) error {
	if p == nil || p.Cfg == nil || p.Repo == nil {
		return errors.New("pipeline: not initialized")
	}
	t, err := p.Repo.Get(taskID)
	if err != nil {
		return err
	}
	if t.Status != model.TaskStatusProcessing {
		return fmt.Errorf("pipeline: want status processing, got %s", t.Status)
	}
	if t.Operation == model.OperationStop {
		return p.executeStop(ctx, t)
	}
	return p.executeStart(ctx, t)
}

func (p *Pipeline) fire(ctx context.Context, event, taskID string) {
	if p == nil || p.CB == nil {
		return
	}
	t, err := p.Repo.Get(taskID)
	if err != nil {
		return
	}
	p.CB.Fire(ctx, event, t)
}

func (p *Pipeline) writeStopRec(ok bool, msg string, st *model.Task, tgt *model.Task) {
	if p == nil || p.Rec == nil || p.Rec.DB == nil {
		return
	}
	detail, _ := json.Marshal(map[string]any{
		"request_task_id":      st.TaskID,
		"target_task_id":       tgt.TaskID,
		"target_status_before": tgt.Status,
	})
	rec := &model.StopRecord{
		RequestTaskID: st.TaskID,
		TargetTaskID:  tgt.TaskID,
		Provider:      st.Provider,
		OK:            ok,
		Message:       msg,
		Detail:        datatypes.JSON(detail),
	}
	_ = p.Rec.CreateStop(rec)
}

func (p *Pipeline) executeStart(ctx context.Context, t *model.Task) error {
	if t.Operation == model.OperationStop {
		return errors.New("pipeline: wrong op")
	}
	err := p.Repo.WithTx(ctx, func(tx *gorm.DB) error {
		r2 := &taskrepo.Repo{DB: tx}
		tt, e := r2.Get(t.TaskID)
		if e != nil {
			return e
		}
		if tt.Status != model.TaskStatusProcessing {
			return fmt.Errorf("concurrent state: %s", tt.Status)
		}
		counts, e := r2.RunningSlotCountsWithDB(tx)
		if e != nil {
			return e
		}
		if ok, _ := scheduler.Admit(p.Cfg.Scheduler, tt, counts); !ok {
			return taskrepo.ErrNotAdmitted
		}
		ok, e := r2.TrySetStatusWithDB(tx, t.TaskID, model.TaskStatusProcessing, model.TaskStatusAdmitted)
		if e != nil {
			return e
		}
		if !ok {
			return errors.New("pipeline: status CAS failed")
		}
		return nil
	})
	if err != nil {
		return err
	}
	p.fire(ctx, callback.EventAdmitted, t.TaskID)

	t2, err := p.Repo.Get(t.TaskID)
	if err != nil {
		return err
	}
	if t2.Provider == "docker" && p.Cfg != nil {
		tasks, err := p.Repo.ListDockerOccupyingGPUTasks()
		if err != nil {
			_ = p.Repo.UpdateStatus(t2.TaskID, model.TaskStatusFailed)
			p.fire(ctx, callback.EventFailed, t2.TaskID)
			return fmt.Errorf("list docker gpu tasks: %w", err)
		}
		if err := docker.CheckDockerGPUOccupancy(p.Cfg.Docker.HostResources, tasks); err != nil {
			_ = p.Repo.UpdateStatus(t2.TaskID, model.TaskStatusFailed)
			p.fire(ctx, callback.EventFailed, t2.TaskID)
			return fmt.Errorf("%w: %s", taskrepo.ErrResourceCheck, err.Error())
		}
	}
	prov, err := p.Reg.Get(t2.Provider)
	if err != nil {
		_ = p.Repo.UpdateStatus(t2.TaskID, model.TaskStatusFailed)
		p.fire(ctx, callback.EventFailed, t2.TaskID)
		return err
	}
	res, err := prov.ResourceCheck(ctx, t2)
	if err != nil {
		_ = p.Repo.UpdateStatus(t2.TaskID, model.TaskStatusFailed)
		p.fire(ctx, callback.EventFailed, t2.TaskID)
		return fmt.Errorf("resource check: %w", err)
	}
	if res == nil || !res.OK {
		reason := "not ok"
		if res != nil && res.Reason != "" {
			reason = res.Reason
		}
		_ = p.Repo.UpdateStatus(t2.TaskID, model.TaskStatusFailed)
		p.fire(ctx, callback.EventFailed, t2.TaskID)
		return fmt.Errorf("%w: %s", taskrepo.ErrResourceCheck, reason)
	}
	runtimeRef, err := prov.Run(ctx, t2)
	if err != nil {
		_ = p.Repo.UpdateStatus(t2.TaskID, model.TaskStatusFailed)
		p.fire(ctx, callback.EventFailed, t2.TaskID)
		return fmt.Errorf("run: %w", err)
	}
	if err := p.Repo.UpdateRuntime(t2.TaskID, model.TaskStatusRunning, runtimeRef); err != nil {
		return err
	}
	p.fire(ctx, callback.EventRunning, t2.TaskID)
	return nil
}

func (p *Pipeline) executeStop(ctx context.Context, t *model.Task) error {
	if t.Operation != model.OperationStop {
		return errors.New("pipeline: not a stop op")
	}
	if t.TargetTaskID == "" {
		return errors.New("pipeline: empty target_task_id")
	}
	target, err := p.Repo.Get(t.TargetTaskID)
	if err != nil {
		_ = p.Repo.UpdateStatus(t.TaskID, model.TaskStatusFailed)
		p.writeStopRec(false, "target not found: "+err.Error(), t, &model.Task{TaskID: t.TargetTaskID, Provider: t.Provider})
		p.fire(ctx, callback.EventFailed, t.TaskID)
		return err
	}
	if target.Operation == model.OperationStop {
		_ = p.Repo.UpdateStatus(t.TaskID, model.TaskStatusFailed)
		p.writeStopRec(false, "target is stop op", t, target)
		p.fire(ctx, callback.EventFailed, t.TaskID)
		return errors.New("pipeline: target must be start workload")
	}
	if target.Status != model.TaskStatusRunning && target.Status != model.TaskStatusAdmitted {
		_ = p.Repo.UpdateStatus(t.TaskID, model.TaskStatusFailed)
		p.writeStopRec(false, "target not stoppable: "+target.Status, t, target)
		p.fire(ctx, callback.EventFailed, t.TaskID)
		return fmt.Errorf("pipeline: target not stoppable: %s", target.Status)
	}
	if t.Provider != target.Provider {
		_ = p.Repo.UpdateStatus(t.TaskID, model.TaskStatusFailed)
		p.writeStopRec(false, "provider mismatch", t, target)
		p.fire(ctx, callback.EventFailed, t.TaskID)
		return errors.New("pipeline: provider mismatch with target")
	}
	prov, err := p.Reg.Get(target.Provider)
	if err != nil {
		_ = p.Repo.UpdateStatus(t.TaskID, model.TaskStatusFailed)
		p.writeStopRec(false, err.Error(), t, target)
		p.fire(ctx, callback.EventFailed, t.TaskID)
		return err
	}
	if err := prov.Stop(ctx, target); err != nil {
		_ = p.Repo.UpdateStatus(t.TaskID, model.TaskStatusFailed)
		p.writeStopRec(false, err.Error(), t, target)
		p.fire(ctx, callback.EventFailed, t.TaskID)
		return fmt.Errorf("stop: %w", err)
	}
	telemetry.FinalFlushDockerLogs(ctx, p.Cfg, p.DockerEng, target)
	if err := p.Repo.UpdateStatus(target.TaskID, model.TaskStatusStopped); err != nil {
		_ = p.Repo.UpdateStatus(t.TaskID, model.TaskStatusFailed)
		p.writeStopRec(false, err.Error(), t, target)
		return err
	}
	if err := p.Repo.UpdateStatus(t.TaskID, model.TaskStatusSucceeded); err != nil {
		return err
	}
	p.writeStopRec(true, "", t, target)
	p.fire(ctx, callback.EventStopped, target.TaskID)
	p.fire(ctx, callback.EventSucceeded, t.TaskID)
	return nil
}

// StartAfterAdmit 兼容名，等同 Dispatch
func (p *Pipeline) StartAfterAdmit(ctx context.Context, taskID string) error {
	return p.Dispatch(ctx, taskID)
}
