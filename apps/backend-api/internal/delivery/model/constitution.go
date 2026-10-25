package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ProjectConstitution retains fixed content and its explicit review lifecycle.
type ProjectConstitution struct {
	TenantID       guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID             guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID      guid.ID    `gorm:"column:project_id;not null"`
	VersionNo      int        `gorm:"column:version_no;not null"`
	Body           string     `gorm:"column:body;not null"`
	BodySHA256     string     `gorm:"column:body_sha256;size:64;not null"`
	State          string     `gorm:"column:state;size:16;not null"`
	ChangeSummary  string     `gorm:"column:change_summary;size:2000;not null"`
	ProposedByKind string     `gorm:"column:proposed_by_kind;size:16;not null"`
	ProposedBy     guid.ID    `gorm:"column:proposed_by;not null"`
	ReviewedBy     *guid.ID   `gorm:"column:reviewed_by"`
	ReviewedAt     *time.Time `gorm:"column:reviewed_at"`
	ReviewComment  *string    `gorm:"column:review_comment;size:2000"`
	ActiveKey      *guid.ID   `gorm:"column:active_key"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	Version        int        `gorm:"column:version;not null"`
}

func (ProjectConstitution) TableName() string { return "delivery_project_constitutions" }
