package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// RegistrationPolicy is the deployment-wide override of account registration.
// Its single row uses ID 1; an absent row preserves deployment configuration.
type RegistrationPolicy struct {
	ID        int       `gorm:"column:id;primaryKey;autoIncrement:false"`
	Enabled   bool      `gorm:"column:enabled;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	UpdatedBy guid.ID   `gorm:"column:updated_by;not null"`
}

func (RegistrationPolicy) TableName() string { return "iam_registration_policy" }
