package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Schedule stores execution progress for a code-declared periodic job.
type Schedule struct {
	ID             guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	Name           string    `gorm:"column:name;size:128;not null"`
	Kind           string    `gorm:"column:kind;size:128;not null"`
	Queue          string    `gorm:"column:queue;size:64;not null"`
	TenantScope    string    `gorm:"column:tenant_scope;size:16;not null"`
	IntervalMS     *int64    `gorm:"column:interval_ms"`
	Cron           *string   `gorm:"column:cron;size:128"`
	Timezone       string    `gorm:"column:timezone;size:64;not null"`
	Payload        string    `gorm:"column:payload;not null"`
	Enabled        bool      `gorm:"column:enabled;not null"`
	MisfirePolicy  string    `gorm:"column:misfire_policy;size:16;not null"`
	NextRunAtMS    int64     `gorm:"column:next_run_at_ms;not null"`
	LastFiredAtMS  *int64    `gorm:"column:last_fired_at_ms"`
	LastJobID      *guid.ID  `gorm:"column:last_job_id"`
	DefinitionHash string    `gorm:"column:definition_hash;size:64;not null"`
	CreatedAt      time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	Version        int       `gorm:"column:version;not null"`
}

func (Schedule) TableName() string { return "background_job_schedules" }
