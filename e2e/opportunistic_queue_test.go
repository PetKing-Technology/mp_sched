package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"mp_sched/internal/database"
	"mp_sched/internal/model"
	"mp_sched/internal/taskrepo"
)

// TestE2EDeferredPendingAllowsBackfill 验证队首进入冷却后，后续 pending 可立即被抢占。
func TestE2EDeferredPendingAllowsBackfill(t *testing.T) {
	cfg := e2eLoadAppConfig(t)
	db, err := database.Open(&cfg.Database)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	providerName := "e2e-backfill-" + uuid.NewString()
	defer db.Unscoped().Where("provider = ?", providerName).Delete(&model.Task{})

	now := time.Now().UTC()
	blocked := &model.Task{
		TaskID:    uuid.NewString(),
		TaskClass: model.TaskClassFast,
		Provider:  providerName,
		Operation: model.OperationStart,
		Status:    model.TaskStatusProcessing,
		CreatedAt: now.Add(-2 * time.Minute),
	}
	next := &model.Task{
		TaskID:    uuid.NewString(),
		TaskClass: model.TaskClassFast,
		Provider:  providerName,
		Operation: model.OperationStart,
		Status:    model.TaskStatusPending,
		CreatedAt: now.Add(-time.Minute),
	}
	repo := &taskrepo.Repo{DB: db}
	if err := repo.Create(blocked); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(next); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeferToPending(blocked.TaskID, "insufficient_gpu_memory", 2*time.Second); err != nil {
		t.Fatal(err)
	}

	claimed, err := repo.ClaimNext(context.Background(), []string{providerName})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.TaskID != next.TaskID {
		t.Fatalf("want backfill task %s, got %s", next.TaskID, claimed.TaskID)
	}
	row, err := repo.Get(blocked.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != model.TaskStatusPending || row.PendingReason != "insufficient_gpu_memory" ||
		row.NextScheduleAt == nil || row.ScheduleAttempts != 1 {
		t.Fatalf("unexpected deferred row: %#v", row)
	}
}
