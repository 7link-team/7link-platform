// Package model contains governance persistence records.
package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// RoleSeed records the permissions previously applied to a managed role.
type RoleSeed struct {
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID        guid.ID   `gorm:"column:tenant_id;not null"`
	RoleKey         string    `gorm:"column:role_key;size:48;not null"`
	RoleID          guid.ID   `gorm:"column:role_id;not null"`
	ManifestVersion int       `gorm:"column:manifest_version;not null"`
	AppliedCodes    string    `gorm:"column:applied_codes;not null"`
	SkippedCodes    string    `gorm:"column:skipped_codes;not null"`
	Managed         bool      `gorm:"column:managed;not null"`
	SeedConflict    bool      `gorm:"column:seed_conflict;not null"`
	Version         int       `gorm:"column:version;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt       time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	CreatedBy       *guid.ID  `gorm:"column:created_by"`
	UpdatedBy       *guid.ID  `gorm:"column:updated_by"`
}

func (RoleSeed) TableName() string { return "governance_role_seeds" }
