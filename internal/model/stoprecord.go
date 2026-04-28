package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// StopRecord 单独记录 stop 操作审计，与 task 行解耦
type StopRecord struct {
	ID             string         `gorm:"type:text;primaryKey" json:"id"`
	RequestTaskID  string         `gorm:"type:text;not null;index" json:"request_task_id"`
	TargetTaskID   string         `gorm:"type:text;not null;index" json:"target_task_id"`
	Provider       string         `gorm:"type:text;not null" json:"provider"`
	OK             bool           `gorm:"not null" json:"ok"`
	Message        string         `gorm:"type:text" json:"message,omitempty"`
	Detail         datatypes.JSON `gorm:"type:jsonb" json:"-"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

func (StopRecord) TableName() string { return "stop_records" }
