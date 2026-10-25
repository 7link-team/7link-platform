package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Version is a complete immutable published snapshot; drafts advance via CAS.
type Version struct {
	Name             string    `gorm:"column:name;not null;size:200;default:''"`
	Origin           string    `gorm:"column:origin;not null;size:16;default:TEAM"`
	BasedOnVersionID *guid.ID  `gorm:"column:based_on_version_id"`
	BasedOnSHA256    string    `gorm:"column:based_on_sha256;not null;size:64;default:''"`
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID   `gorm:"column:tenant_id;not null"`
	Key              string    `gorm:"column:template_key;not null;size:64"`
	Semver           string    `gorm:"column:semver;not null;size:16"`
	State            string    `gorm:"column:state;not null;size:32"`
	Definition       string    `gorm:"column:definition;not null"`
	SHA256           string    `gorm:"column:definition_sha256;not null;size:64"`
	Version          int       `gorm:"column:version;not null"`
	CreatedBy        guid.ID   `gorm:"column:created_by;not null"`
	UpdatedBy        guid.ID   `gorm:"column:updated_by;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Version) TableName() string { return "templates_versions" }

// Binding pins the exact published hash; runs contain only IDs and explicit
// waiting states. Delivery remains the owner of all work-item facts.
type Binding struct {
	Version             int        `gorm:"column:version;not null;default:1"`
	BriefOverrides      string     `gorm:"column:brief_overrides;not null;default:'{}'"`
	OverridesApprovedBy *guid.ID   `gorm:"column:overrides_approved_by"`
	OverridesApprovedAt *time.Time `gorm:"column:overrides_approved_at"`
	ID                  guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID            guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID           guid.ID    `gorm:"column:project_id;not null"`
	TemplateVersionID   guid.ID    `gorm:"column:template_version_id;not null"`
	SHA256              string     `gorm:"column:definition_sha256;not null;size:64"`
	ConstitutionID      guid.ID    `gorm:"column:constitution_id;not null"`
	Runs                string     `gorm:"column:runs;not null"`
	CreatedBy           guid.ID    `gorm:"column:created_by;not null"`
	CreatedAt           time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Binding) TableName() string { return "templates_project_bindings" }
