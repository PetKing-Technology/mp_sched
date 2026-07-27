package database

import (
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

func Open(cfg *config.Database) (*gorm.DB, error) {
	if cfg == nil || cfg.DSN == "" {
		return nil, fmt.Errorf("database: empty DSN")
	}
	logMode := logger.Warn
	if cfg.LogQueries {
		logMode = logger.Info
	}
	g, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		Logger: logger.Default.LogMode(logMode),
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := g.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	return g, nil
}

// Migrate 创建/更新表；移除非独占 GPU 时的旧唯一索引
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(&model.Task{}, &model.StopRecord{}, &model.CallbackDelivery{}); err != nil {
		return err
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	_ = db.Exec(`DROP INDEX IF EXISTS idx_tasks_gpu_lease`)
	_ = db.Exec(`ALTER TABLE IF EXISTS tasks DROP COLUMN IF EXISTS gpu_id`)
	return nil
}
