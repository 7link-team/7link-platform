package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Report struct {
	NeedsRecheck       bool       `gorm:"column:needs_recheck;not null;default:false"`
	LastCheckHash      string     `gorm:"column:last_check_hash;not null;default:''"`
	TenantID           guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                 guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID          guid.ID    `gorm:"column:project_id;not null"`
	PrincipalID        guid.ID    `gorm:"column:principal_id;not null"`
	PeriodKind         string     `gorm:"column:period_kind;not null"`
	PeriodStart        string     `gorm:"column:period_start;not null"`
	State              string     `gorm:"column:state;not null"`
	Content            string     `gorm:"column:content;not null"`
	ContentHash        string     `gorm:"column:content_hash;not null"`
	Version            int        `gorm:"column:version;not null"`
	CreatedBy          guid.ID    `gorm:"column:created_by;not null"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	Facts              string     `gorm:"column:facts;not null"`
	ConfirmedBy        *guid.ID   `gorm:"column:confirmed_by"`
	ConfirmedAt        *time.Time `gorm:"column:confirmed_at"`
	DueAt              *time.Time `gorm:"column:due_at"`
	OverdueNoticeCount int        `gorm:"column:overdue_notice_count;not null;default:0"`
	SubmittedLate      bool       `gorm:"column:submitted_late;not null;default:false"`
}

func (Report) TableName() string { return "planning_reports" }

// ReportSubmission is an immutable submission receipt, independent of later
// returns, edits, confirmations and resubmissions of the report aggregate.
type ReportSubmission struct {
	TenantID      guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID            guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ReportID      guid.ID    `gorm:"column:report_id;not null"`
	ReportVersion int        `gorm:"column:report_version;not null"`
	SubmittedAt   time.Time  `gorm:"column:submitted_at;not null;autoCreateTime:false"`
	DueAt         *time.Time `gorm:"column:due_at"`
}

func (ReportSubmission) TableName() string { return "planning_report_submissions" }

// ReportCheck preserves the result actually observed by a reconciliation call.
// It never replaces the report state or its editable version.
type ReportCheck struct {
	TenantID      guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID            guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ReportID      guid.ID   `gorm:"column:report_id;not null"`
	ReportVersion int       `gorm:"column:report_version;not null"`
	Result        string    `gorm:"column:result;not null"`
	CheckedAt     time.Time `gorm:"column:checked_at;not null;autoCreateTime:false"`
}

func (ReportCheck) TableName() string { return "planning_report_checks" }
