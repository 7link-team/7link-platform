// Package model contains the audit module's persisted row types.
package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type Event struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null"`
	PrincipalID  *guid.ID  `gorm:"column:principal_id"`
	EventType    string    `gorm:"column:event_type;size:64;not null"`
	TargetType   string    `gorm:"column:target_type;size:64;not null"`
	TargetID     *guid.ID  `gorm:"column:target_id"`
	Outcome      string    `gorm:"column:outcome;size:16;not null"`
	IPAddress    string    `gorm:"column:ip_address;size:64;not null"`
	UserAgent    string    `gorm:"column:user_agent;size:512;not null"`
	Detail       string    `gorm:"column:detail;type:text;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	Sequence     int64     `gorm:"column:sequence;not null"`
	PreviousHash string    `gorm:"column:previous_hash;size:64;not null"`
	EventHash    string    `gorm:"column:event_hash;size:64;not null"`
}

func (Event) TableName() string { return "iam_audit_events" }

type Head struct {
	TenantID  guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	Sequence  int64     `gorm:"column:sequence;not null"`
	EventHash string    `gorm:"column:event_hash;size:64;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (Head) TableName() string { return "iam_audit_heads" }

type RetentionPolicy struct {
	TenantID         guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	MinDays          int       `gorm:"column:min_days;not null"`
	ArchiveAfterDays int       `gorm:"column:archive_after_days;not null"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null"`
}

func (RetentionPolicy) TableName() string { return "iam_audit_retention_policies" }

// ArchiveJob retains its frozen export and immutable object reference across
// process restarts. Lease/retry timestamps use the shared database clock.
type ArchiveJob struct {
	ID            guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID      guid.ID   `gorm:"column:tenant_id;not null"`
	AfterSequence int64     `gorm:"column:after_sequence;not null"`
	HeadSequence  int64     `gorm:"column:head_sequence;not null"`
	Snapshot      string    `gorm:"column:snapshot;type:text;not null"`
	State         string    `gorm:"column:state;size:16;not null"`
	Attempts      int64     `gorm:"column:attempts;not null"`
	LeaseToken    guid.ID   `gorm:"column:lease_token;not null"`
	LeaseUntil    int64     `gorm:"column:lease_until;not null"`
	NextAttempt   int64     `gorm:"column:next_attempt;not null"`
	ErrorCode     string    `gorm:"column:error_code;size:32;not null"`
	Object        string    `gorm:"column:object;type:text;not null"`
	RetainUntil   time.Time `gorm:"column:retain_until;not null"`
	CreatedAt     time.Time `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null"`
}

func (ArchiveJob) TableName() string { return "iam_audit_archive_jobs" }
