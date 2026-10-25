package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// Report is the serialization head of one logical subject/scheme/period report.
type Report struct {
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	SchemeID    guid.ID   `gorm:"column:scheme_id;not null"`
	SubjectKind string    `gorm:"column:subject_kind;not null;size:16"`
	SubjectID   guid.ID   `gorm:"column:subject_id;not null"`
	Cadence     string    `gorm:"column:cadence;not null;size:16"`
	PeriodStart string    `gorm:"column:period_start;not null;size:10"`
	PeriodEnd   string    `gorm:"column:period_end;not null;size:10"`
	Version     int       `gorm:"column:version;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Report) TableName() string { return "performance_reports" }

// ReportRevision is append-only, including superseded confirmations.
type ReportRevision struct {
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID  guid.ID   `gorm:"column:tenant_id;not null"`
	ReportID  guid.ID   `gorm:"column:report_id;not null"`
	Version   int       `gorm:"column:version;not null"`
	Snapshot  string    `gorm:"column:snapshot;not null"`
	SHA256    string    `gorm:"column:sha256;not null;size:64"`
	Reason    string    `gorm:"column:reason;not null;size:2000"`
	CreatedBy guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ReportRevision) TableName() string { return "performance_report_revisions" }
