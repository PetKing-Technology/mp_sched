package controller

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"mp_sched/internal/model"
	"mp_sched/internal/pipeline"
	"mp_sched/internal/provider"
	"mp_sched/internal/taskrepo"
)

// Request 与 JSON 体一致
type Request struct {
	Operation         string          `json:"operation"`
	TaskClass         string          `json:"task_class"`
	Provider          string          `json:"provider"`
	TargetTaskID      string          `json:"target_task_id"`
	ResCPU            string          `json:"res_cpu"`
	ResMemory         string          `json:"res_memory"`
	ResGPU            string          `json:"res_gpu"`
	MaxRuntimeSeconds int             `json:"max_runtime_seconds,omitempty"`
	Business          json.RawMessage `json:"business"`
	// Image 容器镜像，docker 主入口；可省略，由 business.image 补全
	Image string `json:"image"`
}

// Handlers 入参只校验+入库
type Handlers struct {
	Pl   *pipeline.Pipeline
	Reg  provider.Registry
	Repo *taskrepo.Repo
}

// Validate 检查必需字段与 provider 名
func (h *Handlers) Validate(req *Request) error {
	if req == nil {
		return errors.New("empty body")
	}
	op := req.Operation
	if op == "" {
		op = model.OperationStart
	}
	if op != model.OperationStart && op != model.OperationStop {
		return errors.New("invalid operation")
	}
	if req.Provider == "" {
		return errors.New("provider required")
	}
	if _, err := h.Reg.Get(req.Provider); err != nil {
		return errors.New("unknown provider: " + req.Provider)
	}
	if op == model.OperationStop && req.TargetTaskID == "" {
		return errors.New("target_task_id required for stop")
	}
	return nil
}

// Enqueue 校验后落库为 pending。排队无上限，不想等时由用户主动 stop
func (h *Handlers) Enqueue(ctx context.Context, req *Request) (*model.Task, error) {
	if err := h.Validate(req); err != nil {
		return nil, err
	}
	op := req.Operation
	if op == "" {
		op = model.OperationStart
	}
	bz := []byte(`{}`)
	if len(req.Business) > 0 {
		bz = req.Business
	}
	img := strings.TrimSpace(req.Image)
	t, err := h.Pl.Submit(ctx, pipeline.SubmitInput{
		Business:          bz,
		TaskClass:         req.TaskClass,
		ProviderName:      req.Provider,
		Operation:         op,
		TargetTaskID:      req.TargetTaskID,
		Image:             img,
		ResCPU:            req.ResCPU,
		ResMemory:         req.ResMemory,
		ResGPU:            req.ResGPU,
		MaxRuntimeSeconds: req.MaxRuntimeSeconds,
	})
	if err != nil {
		return nil, err
	}
	slog.Info("controller enqueue", "task_id", t.TaskID, "operation", t.Operation, "provider", t.Provider, "task_class", t.TaskClass)
	return t, nil
}

// EnqueueStopForTarget 根据已有 workload 的 task_id 提交一条 stop 请求（provider 等从目标行继承）
func (h *Handlers) EnqueueStopForTarget(ctx context.Context, targetTaskID string) (*model.Task, error) {
	if h.Repo == nil {
		return nil, errors.New("repo not configured")
	}
	tgt, err := h.Repo.Get(targetTaskID)
	if err != nil {
		return nil, err
	}
	if tgt.Operation == model.OperationStop {
		return nil, errors.New("target is a stop request, not a start workload")
	}
	if _, err := h.Reg.Get(tgt.Provider); err != nil {
		return nil, err
	}
	st, err := h.Pl.Submit(ctx, pipeline.SubmitInput{
		Operation:    model.OperationStop,
		TaskClass:    tgt.TaskClass,
		ProviderName: tgt.Provider,
		TargetTaskID: tgt.TaskID,
		ResCPU:       "",
		ResMemory:    "",
		ResGPU:       "",
		Business:     []byte(`{}`),
	})
	if err != nil {
		return nil, err
	}
	slog.Info("controller stop", "task_id", st.TaskID, "target_task_id", targetTaskID, "provider", tgt.Provider)
	return st, nil
}
