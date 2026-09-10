package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ClosureRequest stores workflow metadata only; no applicant or reviewer PII.
type ClosureRequest struct {
	PrincipalID guid.ID   `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	Status      string    `gorm:"column:status;size:16;not null"`
	Version     int64     `gorm:"column:version;not null"`
	RequestedAt time.Time `gorm:"column:requested_at;not null"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null"`
}

func (ClosureRequest) TableName() string { return "iam_closure_requests" }
