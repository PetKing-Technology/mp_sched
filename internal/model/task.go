package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// TaskClass 仅用于调度器区分快慢，不做业务侧校验；调用方可随意扩展，调度只认常量和字符串。
const (
	TaskClassFast = "fast"
	TaskClassSlow = "slow"
)

// Operation 任务意图：启动工作负载，或向已有 workload 发停止
const (
	OperationStart = "start"
	OperationStop  = "stop"
)

// TaskStatus 本地生命周期状态（可随你们 controller 再细化）
const (
	TaskStatusPending    = "pending"    // controller 入库
	TaskStatusProcessing = "processing" // worker 已抢占，尚未完成调度首步
	TaskStatusAdmitted   = "admitted"   // 已通过资源/策略准入
	TaskStatusRunning    = "running"
	TaskStatusSucceeded  = "succeeded"
	TaskStatusFailed     = "failed"
	TaskStatusStopped    = "stopped"
)

// Task 在本地库中的主记录。task_id 由本服务生成并维护主键；business 为 JSON，docker 下可含 config_oss_key 等。
type Task struct {
	TaskID string `gorm:"type:text;primaryKey" json:"task_id"`

	Business datatypes.JSON `gorm:"type:jsonb;not null" json:"-"`

	// Image 容器镜像（docker 主入口）；可与 business.image 二选一，本字段优先
	Image string `gorm:"type:text" json:"image,omitempty"`

	// Class 快/慢 用于调度与准入；可扩展其它取值，需同步调度器约定
	TaskClass string `gorm:"type:text;not null;index" json:"task_class"`

	// Provider 如 docker / k8s
	Provider string `gorm:"type:text;not null;index" json:"provider"`

	// Operation 启动新 workload，或对 TargetTaskID 发停止（停止类请求本身会生成新的 task 行，便于审计）
	Operation string `gorm:"type:text;not null" json:"operation"`

	// TargetTaskID 当 Operation=stop 时，要停止的「start 类任务」的 task_id
	TargetTaskID string `gorm:"type:text;index" json:"target_task_id,omitempty"`

	// ResCPU/ResMemory 任务请求的算力（k8s 风格字符串），在任务体上显式传递
	ResCPU    string `gorm:"type:text" json:"res_cpu,omitempty"`
	ResMemory string `gorm:"type:text" json:"res_memory,omitempty"`
	// ResGPU 是否使用 GPU：仅 1/true/yes/on（不区分大小写）为开；NVIDIA device id 仅来自 docker.host_resources.gpu_ids（去重后下发）
	ResGPU string `gorm:"type:text" json:"res_gpu,omitempty"`

	// Extra 为不透明 JSON，存 placement、回调摘要、资源快照等
	Extra datatypes.JSON `gorm:"type:jsonb" json:"-"`

	// MaxRuntimeSeconds 自进入 running 起最大运行秒数。0=使用 worker 配置 default_max_runtime_seconds；正数=覆盖；负数=不限制。
	MaxRuntimeSeconds int `gorm:"default:0" json:"max_runtime_seconds,omitempty"`

	// RunningAt 首次变为 running 的时间（UTC），用于运行超时扫描；由 UpdateRuntime 写入。
	RunningAt *time.Time `gorm:"index" json:"running_at,omitempty"`

	Status string `gorm:"type:text;not null;index" json:"status"`

	// RuntimeRef Provider 侧引用（容器 ID、Job 名等）
	RuntimeRef string `gorm:"type:text" json:"runtime_ref,omitempty"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Task) TableName() string { return "tasks" }
