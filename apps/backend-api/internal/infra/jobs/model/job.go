package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Job is the durable queue record. Lease times use database-clock milliseconds;
// presentation timestamps retain UTC datetime precision across all dialects.
type Job struct {
	ID             guid.ID `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID       guid.ID `gorm:"column:tenant_id;not null"`
	Queue          string  `gorm:"column:queue;size:64;not null"`
	Kind           string  `gorm:"column:kind;size:128;not null"`
	State          string  `gorm:"column:state;size:16;not null"`
	Priority       int     `gorm:"column:priority;not null"`
	Payload        string  `gorm:"column:payload;not null"`
	IdempotencyKey *string `gorm:"column:idempotency_key;size:128"`
	ConcurrencyKey *string `gorm:"column:concurrency_key;size:128"`
	// Fanout dispatch metadata is separate from payload so a full 64 KiB payload
	// stays intact. The schedule name is frozen in ConcurrencyKey.
	FanoutKind       *string    `gorm:"column:fanout_kind;size:128"`
	FanoutFireMS     *int64     `gorm:"column:fanout_fire_ms"`
	Attempts         int        `gorm:"column:attempts;not null"`
	MaxAttempts      int        `gorm:"column:max_attempts;not null"`
	RunAtMS          int64      `gorm:"column:run_at_ms;not null"`
	LeaseOwner       *string    `gorm:"column:lease_owner;size:64"`
	LeaseToken       int64      `gorm:"column:lease_token;not null"`
	LeaseExpiresAtMS *int64     `gorm:"column:lease_expires_at_ms"`
	TimeoutMS        int64      `gorm:"column:timeout_ms;not null"`
	LastErrorCode    string     `gorm:"column:last_error_code;size:64;not null"`
	LastError        string     `gorm:"column:last_error;size:1024;not null"`
	ErrorHistory     string     `gorm:"column:error_history;not null"`
	ManualRetries    int        `gorm:"column:manual_retries;not null"`
	CorrelationID    string     `gorm:"column:correlation_id;size:64;not null"`
	CreatedBy        *guid.ID   `gorm:"column:created_by"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt        time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	StartedAt        *time.Time `gorm:"column:started_at"`
	FinishedAt       *time.Time `gorm:"column:finished_at"`
	Version          int        `gorm:"column:version;not null"`
}

func (Job) TableName() string { return "background_jobs" }
