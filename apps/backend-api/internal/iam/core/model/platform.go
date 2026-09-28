package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type PlatformAdministrator struct {
	PrincipalID guid.ID   `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
}

func (PlatformAdministrator) TableName() string { return "iam_platform_administrators" }

type PlatformGrant struct {
	PrincipalID    guid.ID   `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	PermissionCode string    `gorm:"column:permission_code;primaryKey;size:128"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
}

func (PlatformGrant) TableName() string { return "iam_platform_grants" }
