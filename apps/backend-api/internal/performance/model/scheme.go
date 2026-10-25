package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Scheme is only a serialization head; all configuration lives in immutable revisions.
type Scheme struct {
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID  guid.ID   `gorm:"column:tenant_id;not null"`
	Version   int       `gorm:"column:version;not null"`
	Enabled   bool      `gorm:"column:enabled;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Scheme) TableName() string { return "performance_schemes" }

// Revision binds rules, report template, scope and enablement to one immutable ID.
type Revision struct {
	ID         guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID   guid.ID   `gorm:"column:tenant_id;not null"`
	SchemeID   guid.ID   `gorm:"column:scheme_id;not null"`
	Version    int       `gorm:"column:version;not null"`
	Definition string    `gorm:"column:definition;not null"`
	SHA256     string    `gorm:"column:sha256;not null;size:64"`
	Reason     string    `gorm:"column:reason;not null;size:2000"`
	CreatedBy  guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Revision) TableName() string { return "performance_scheme_revisions" }
