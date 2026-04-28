package provider

import (
	"context"
	"fmt"

	"mp_sched/internal/model"
)

// ResourceCheckResult 资源是否满足当前任务在 Provider 上的运行条件。
type ResourceCheckResult struct {
	OK     bool
	Reason string
}

// RuntimeStatus 从 Provider 查询到的执行态，供状态机与回调使用。
type RuntimeStatus struct {
	Phase   string // running, succeeded, failed, unknown
	Message string
}

// Provider 抽象 Docker / K8s 等后端的可插拔能力；全生命周期中关键步：查资源、起任务、查状态。
type Provider interface {
	Name() string

	// ResourceCheck 在真正 Run 前与周期性健康检查中调用，由实现查询集群/本机可分配量
	ResourceCheck(ctx context.Context, t *model.Task) (*ResourceCheckResult, error)

	// Run 根据本地 Task 记录启动工作负载，返回 Provider 侧引用 id/name
	Run(ctx context.Context, t *model.Task) (runtimeRef string, err error)

	// Status 轮询或查询当前执行结果
	Status(ctx context.Context, t *model.Task) (*RuntimeStatus, error)

	// Stop 停止/删除 Provider 上的工作负载，t 为**被定位的工作负载**（start 行），不是 stop 申请行
	Stop(ctx context.Context, t *model.Task) error
}

// Registry 按 name 取 Provider
type Registry map[string]Provider

func (r Registry) Get(name string) (Provider, error) {
	p, ok := r[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %q", name)
	}
	return p, nil
}
