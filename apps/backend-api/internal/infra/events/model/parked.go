package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Parked struct {
	ID              guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	Consumer        string     `gorm:"column:consumer;size:128;not null"`
	PartitionNo     int        `gorm:"column:partition_no;not null"`
	EventID         guid.ID    `gorm:"column:event_id;not null"`
	StreamSeq       int64      `gorm:"column:stream_seq;not null"`
	TenantID        guid.ID    `gorm:"column:tenant_id;not null"`
	SubjectKind     string     `gorm:"column:subject_kind;size:64;not null"`
	SubjectID       guid.ID    `gorm:"column:subject_id;not null"`
	Reason          string     `gorm:"column:reason;size:16;not null"`
	State           string     `gorm:"column:state;size:16;not null"`
	Attempts        int        `gorm:"column:attempts;not null"`
	NextAttemptAtMS *int64     `gorm:"column:next_attempt_at_ms"`
	LastErrorCode   string     `gorm:"column:last_error_code;size:64;not null"`
	LastError       string     `gorm:"column:last_error;size:2000;not null"`
	ResolvedBy      *guid.ID   `gorm:"column:resolved_by"`
	ResolutionNote  string     `gorm:"column:resolution_note;size:2000;not null"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	ResolvedAt      *time.Time `gorm:"column:resolved_at"`
	Version         int        `gorm:"column:version;not null"`
}

func (Parked) TableName() string { return "domain_event_parked" }
