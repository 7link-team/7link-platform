package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// InboxAcknowledgement freezes one recipient's acknowledgement of one source revision.
type InboxAcknowledgement struct {
	ID             guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID       guid.ID   `gorm:"column:tenant_id;not null"`
	PrincipalID    guid.ID   `gorm:"column:principal_id;not null"`
	Kind           string    `gorm:"column:kind;size:64;not null"`
	SourceID       guid.ID   `gorm:"column:source_id;not null"`
	SourceRevision string    `gorm:"column:source_revision;size:64;not null"`
	CreatedAt      time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (InboxAcknowledgement) TableName() string { return "governance_inbox_acknowledgements" }
