package recordrepo

import (
	"errors"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"mp_sched/internal/model"
)

// Repo stop_records 表
type Repo struct {
	DB *gorm.DB
}

// CreateStop 写入一次 stop 审计
func (r *Repo) CreateStop(rec *model.StopRecord) error {
	if r == nil || r.DB == nil {
		return errors.New("recordrepo: nil db")
	}
	if rec == nil {
		return errors.New("recordrepo: nil record")
	}
	if rec.ID == "" {
		rec.ID = uuid.NewString()
	}
	if len(rec.Detail) == 0 {
		rec.Detail = datatypes.JSON(`{}`)
	}
	return r.DB.Create(rec).Error
}
