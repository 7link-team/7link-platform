package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// SignedBrief freezes the single Human-signed confirmation sheet for a workflow binding.
type SignedBrief struct {
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID  guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID guid.ID   `gorm:"column:project_id;not null"`
	IntakeID  guid.ID   `gorm:"column:intake_id;not null"`
	BindingID guid.ID   `gorm:"column:binding_id;not null"`
	ItemsJSON string    `gorm:"column:items_json;not null"`
	SHA256    string    `gorm:"column:sha256;size:64;not null"`
	SignedBy  guid.ID   `gorm:"column:signed_by;not null"`
	SignedAt  time.Time `gorm:"column:signed_at;not null;autoCreateTime:false"`
}

func (SignedBrief) TableName() string { return "intake_signed_briefs" }
