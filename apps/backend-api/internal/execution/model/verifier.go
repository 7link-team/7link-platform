package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Verifier is the tenant registry aggregate; executable definitions live in immutable versions.
type Verifier struct {
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID         guid.ID   `gorm:"column:tenant_id;not null"`
	Key              string    `gorm:"column:key;size:64;not null"`
	Name             string    `gorm:"column:name;size:200;not null"`
	Layer            string    `gorm:"column:layer;size:16;not null"`
	State            string    `gorm:"column:state;size:16;not null"`
	CurrentVersionNo int       `gorm:"column:current_version_no;not null"`
	AppliesTo        string    `gorm:"column:applies_to;not null"`
	Required         bool      `gorm:"column:required;not null"`
	Version          int       `gorm:"column:version;not null"`
	CreatedBy        guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Verifier) TableName() string { return "execution_verifiers" }

type VerifierVersion struct {
	ID         guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID   guid.ID   `gorm:"column:tenant_id;not null"`
	VerifierID guid.ID   `gorm:"column:verifier_id;not null"`
	VersionNo  int       `gorm:"column:version_no;not null"`
	Spec       string    `gorm:"column:spec;not null"`
	TimeoutMS  int64     `gorm:"column:timeout_ms;not null"`
	ChangeNote string    `gorm:"column:change_note;size:2000;not null"`
	CreatedBy  guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (VerifierVersion) TableName() string { return "execution_verifier_versions" }
