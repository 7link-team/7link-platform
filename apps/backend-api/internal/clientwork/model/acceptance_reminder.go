package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// AcceptanceReminder freezes the schedule at actual in-app publication.
// An emitted notice is never a delivery receipt or client acceptance.
type AcceptanceReminder struct {
	TenantID       guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID             guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	ProjectID      guid.ID    `gorm:"column:project_id;not null"`
	PackageID      guid.ID    `gorm:"column:package_id;not null"`
	PackageVersion int        `gorm:"column:package_version;not null"`
	ContentHash    string     `gorm:"column:content_hash;not null"`
	RecipientID    guid.ID    `gorm:"column:recipient_id;not null"`
	Day            int        `gorm:"column:day_no;not null"`
	Kind           string     `gorm:"column:kind;not null"`
	PublishedAt    time.Time  `gorm:"column:published_at;not null;autoCreateTime:false"`
	DueAt          time.Time  `gorm:"column:due_at;not null;autoCreateTime:false"`
	EmittedAt      *time.Time `gorm:"column:emitted_at"`
	CancelledAt    *time.Time `gorm:"column:cancelled_at"` //nolint:misspell // Preserve the published domain or serialized contract spelling.
}

func (AcceptanceReminder) TableName() string { return "clientwork_acceptance_reminders" }
