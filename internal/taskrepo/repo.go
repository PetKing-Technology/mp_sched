package taskrepo

import (
	"context"
	"errors"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"mp_sched/internal/model"
	"mp_sched/internal/scheduler"
)

// SlotStatuses 占用并发槽位的 start 工作负载
var SlotStatuses = []string{model.TaskStatusAdmitted, model.TaskStatusRunning}

// Repo 本地 Task 持久化
type Repo struct {
	DB *gorm.DB
}

// opIsStartWorkload: stop 行不占槽
func opIsStart() string {
	return "COALESCE(NULLIF(TRIM(operation), ''), 'start') != ?"
}

// RunningSlotCounts 仅统计会占用资源的 start 工作负载
func (r *Repo) RunningSlotCounts() (*scheduler.RunningCounts, error) {
	if r == nil || r.DB == nil {
		return nil, errors.New("taskrepo: nil db")
	}
	return r.RunningSlotCountsWithDB(r.DB)
}

// RunningSlotCountsWithDB 支持事务内统计
func (r *Repo) RunningSlotCountsWithDB(db *gorm.DB) (*scheduler.RunningCounts, error) {
	if db == nil {
		return nil, errors.New("taskrepo: nil db")
	}
	var total int64
	if err := db.Model(&model.Task{}).Where("status IN ?", SlotStatuses).Where(
		opIsStart(), model.OperationStop,
	).Count(&total).Error; err != nil {
		return nil, err
	}
	var fast int64
	if err := db.Model(&model.Task{}).Where("status IN ?", SlotStatuses).Where(
		opIsStart(), model.OperationStop,
	).Where("task_class = ?", model.TaskClassFast).Count(&fast).Error; err != nil {
		return nil, err
	}
	var slow int64
	if err := db.Model(&model.Task{}).Where("status IN ?", SlotStatuses).Where(
		opIsStart(), model.OperationStop,
	).Where("task_class = ?", model.TaskClassSlow).Count(&slow).Error; err != nil {
		return nil, err
	}
	return &scheduler.RunningCounts{Total: int(total), Fast: int(fast), Slow: int(slow)}, nil
}

// ListDockerOccupyingGPUTasks provider=docker 且 admitted|running 的 start 任务（用于 GPU 槽位占用汇总）
func (r *Repo) ListDockerOccupyingGPUTasks() ([]model.Task, error) {
	if r == nil || r.DB == nil {
		return nil, errors.New("taskrepo: nil db")
	}
	var out []model.Task
	err := r.DB.
		Where("provider = ?", "docker").
		Where("status IN ?", SlotStatuses).
		Where(opIsStart(), model.OperationStop).
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListDockerRunning provider=docker 且 running、且已有 runtime_ref 的 start 任务（用于资源与日志采集）
func (r *Repo) ListDockerRunning() ([]model.Task, error) {
	if r == nil || r.DB == nil {
		return nil, errors.New("taskrepo: nil db")
	}
	var out []model.Task
	q := r.DB.
		Where("provider = ? AND status = ?", "docker", model.TaskStatusRunning).
		Where("COALESCE(NULLIF(TRIM(operation), ''), 'start') != ?", model.OperationStop).
		Where("COALESCE(NULLIF(TRIM(runtime_ref), ''), '') != ''")
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// ListStartByStatuses 对账/轮询：按 operation 为 start 的任务
func (r *Repo) ListStartByStatuses(stas []string) ([]model.Task, error) {
	if r == nil || r.DB == nil {
		return nil, errors.New("taskrepo: nil db")
	}
	var out []model.Task
	if err := r.DB.Where("status IN ?", stas).Where(opIsStart(), model.OperationStop).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// terminalStartStatuses 终态的 start 工作负载，用于与 runtime 对账/孤儿回收
var terminalStartStatuses = []string{model.TaskStatusSucceeded, model.TaskStatusFailed, model.TaskStatusStopped}

// ListTerminalDockerWithRuntimeRef 库中已终态、仍有 runtime_ref 的 docker start 行（与容器仍可能不一致时由 reconciler 处理）
func (r *Repo) ListTerminalDockerWithRuntimeRef() ([]model.Task, error) {
	if r == nil || r.DB == nil {
		return nil, errors.New("taskrepo: nil db")
	}
	var out []model.Task
	err := r.DB.
		Where("provider = ?", "docker").
		Where("status IN ?", terminalStartStatuses).
		Where(opIsStart(), model.OperationStop).
		Where("COALESCE(NULLIF(TRIM(runtime_ref), ''), '') != ''").
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ClearRuntimeRef 将 runtime_ref 置空（不修改 status），用于对账/孤儿回收
func (r *Repo) ClearRuntimeRef(taskID string) error {
	if r == nil || r.DB == nil {
		return errors.New("taskrepo: nil db")
	}
	if taskID == "" {
		return errors.New("taskrepo: empty task_id")
	}
	return r.DB.Model(&model.Task{}).Where("task_id = ?", taskID).Update("runtime_ref", "").Error
}

// ListFilter 列表查询
type ListFilter struct {
	Status   string
	Provider string
	Limit    int
	Offset   int
}

// ListTasks 按状态/提供方分页
func (r *Repo) ListTasks(f ListFilter) ([]model.Task, int64, error) {
	if r == nil || r.DB == nil {
		return nil, 0, errors.New("taskrepo: nil db")
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	scope := func(db *gorm.DB) *gorm.DB {
		if f.Status != "" {
			db = db.Where("status = ?", f.Status)
		}
		if f.Provider != "" {
			db = db.Where("provider = ?", f.Provider)
		}
		return db
	}
	var total int64
	if err := r.DB.Model(&model.Task{}).Scopes(scope).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []model.Task
	if err := r.DB.Model(&model.Task{}).Scopes(scope).Order("created_at DESC").Limit(f.Limit).Offset(f.Offset).Find(&out).Error; err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ClaimNext 将最老的一条 pending 置为 processing（SKIP LOCKED 便于多 worker）
// providers 非空时只抢占这些 provider
func (r *Repo) ClaimNext(ctx context.Context, providers []string) (*model.Task, error) {
	if r == nil || r.DB == nil {
		return nil, errors.New("taskrepo: nil db")
	}
	var out *model.Task
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t model.Task
		q := tx.
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ?", model.TaskStatusPending)
		if len(providers) > 0 {
			q = q.Where("provider IN ?", providers)
		}
		if err := q.
			Order("created_at ASC").
			First(&t).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return gorm.ErrRecordNotFound
			}
			return err
		}
		res := tx.Model(&model.Task{}).Where("task_id = ? AND status = ?", t.TaskID, model.TaskStatusPending).Update("status", model.TaskStatusProcessing)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		t.Status = model.TaskStatusProcessing
		out = &t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ResetToPending 从 processing 退回 pending（可重试）
func (r *Repo) ResetToPending(taskID string) error {
	if taskID == "" {
		return errors.New("taskrepo: empty task_id")
	}
	return r.DB.Model(&model.Task{}).Where("task_id = ? AND status = ?", taskID, model.TaskStatusProcessing).Update("status", model.TaskStatusPending).Error
}

// WithTx 事务
func (r *Repo) WithTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if r == nil || r.DB == nil {
		return errors.New("taskrepo: nil db")
	}
	return r.DB.WithContext(ctx).Transaction(fn)
}

// TrySetStatus 用于 CAS：仅当当前状态=from 时改为 to
func (r *Repo) TrySetStatusWithDB(db *gorm.DB, taskID, from, to string) (bool, error) {
	if taskID == "" {
		return false, errors.New("taskrepo: empty task_id")
	}
	res := db.Model(&model.Task{}).Where("task_id = ? AND status = ?", taskID, from).Update("status", to)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// Create 插入新任务
func (r *Repo) Create(t *model.Task) error {
	if t == nil {
		return errors.New("taskrepo: nil task")
	}
	if len(t.Business) == 0 {
		t.Business = datatypes.JSON(`{}`)
	}
	if t.Extra == nil {
		t.Extra = datatypes.JSON(`{}`)
	}
	if t.Operation == "" {
		t.Operation = model.OperationStart
	}
	return r.DB.Create(t).Error
}

// Get 按主键
func (r *Repo) Get(taskID string) (*model.Task, error) {
	var t model.Task
	if err := r.DB.First(&t, "task_id = ?", taskID).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// Update 全量或部分更新（GORM 会只更新非零；RuntimeRef/Status 常用）
func (r *Repo) Update(t *model.Task) error {
	if t == nil || t.TaskID == "" {
		return errors.New("taskrepo: invalid task")
	}
	return r.DB.Model(&model.Task{}).Where("task_id = ?", t.TaskID).Updates(t).Error
}

// UpdateStatus 仅改状态
func (r *Repo) UpdateStatus(taskID, status string) error {
	if taskID == "" {
		return errors.New("taskrepo: empty task_id")
	}
	return r.DB.Model(&model.Task{}).Where("task_id = ?", taskID).Update("status", status).Error
}

// UpdateRuntime 启动后回写 running，并记录 RunningAt 供运行超时扫描
func (r *Repo) UpdateRuntime(taskID, status, runtimeRef string) error {
	if r == nil || r.DB == nil {
		return errors.New("taskrepo: nil db")
	}
	if taskID == "" {
		return errors.New("taskrepo: empty task_id")
	}
	now := time.Now().UTC()
	return r.DB.Model(&model.Task{}).Where("task_id = ?", taskID).Updates(map[string]any{
		"status":      status,
		"runtime_ref": runtimeRef,
		"running_at":  now,
	}).Error
}

// ListRunningStartWorkloadsWithRef status=running 的 start 任务且已有 runtime_ref（供运行超时扫描）
func (r *Repo) ListRunningStartWorkloadsWithRef() ([]model.Task, error) {
	if r == nil || r.DB == nil {
		return nil, errors.New("taskrepo: nil db")
	}
	var out []model.Task
	err := r.DB.
		Where("status = ?", model.TaskStatusRunning).
		Where(opIsStart(), model.OperationStop).
		Where("COALESCE(NULLIF(TRIM(runtime_ref), ''), '') != ''").
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ErrNotAdmitted 策略或资源未通过
var ErrNotAdmitted = errors.New("task not admitted by scheduler policy")

// ErrResourceCheck 资源预检失败
var ErrResourceCheck = errors.New("provider resource check failed")

// RevertAdmittedStuck 将超时的 admitted 标为 failed（由 worker 定时调用）
func (r *Repo) RevertAdmittedStuck(before time.Time) (int64, error) {
	if r == nil || r.DB == nil {
		return 0, errors.New("taskrepo: nil db")
	}
	res := r.DB.Model(&model.Task{}).Where("status = ? AND updated_at < ?", model.TaskStatusAdmitted, before).Update("status", model.TaskStatusFailed)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
