package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ReviewPlan is immutable proposal content with a separately recorded review.
// Human source is explicit; no execution receipt is fabricated.
type ReviewPlan struct {
	ID          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	TenantID    guid.ID    `gorm:"column:tenant_id;not null" json:"tenant_id"`
	ProjectID   guid.ID    `gorm:"column:project_id;not null" json:"project_id"`
	WorkItemID  guid.ID    `gorm:"column:work_item_id;not null" json:"work_item_id"`
	ProposedBy  guid.ID    `gorm:"column:proposed_by;not null" json:"proposed_by"`
	SourceKind  string     `gorm:"column:source_kind;not null" json:"source_kind"`
	Content     string     `gorm:"column:content;not null" json:"-"`
	ContentHash string     `gorm:"column:content_hash;not null" json:"content_hash"`
	CoverageOK  bool       `gorm:"column:coverage_ok;not null" json:"coverage_ok"`
	Round       int        `gorm:"column:round;not null" json:"round"`
	State       string     `gorm:"column:state;not null" json:"state"`
	Feedback    *string    `gorm:"column:feedback" json:"feedback,omitempty"`
	DecidedBy   *guid.ID   `gorm:"column:decided_by" json:"decided_by,omitempty"`
	DecidedAt   *time.Time `gorm:"column:decided_at" json:"decided_at,omitempty"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
	Version     int        `gorm:"column:version;not null" json:"version"`
}

func (ReviewPlan) TableName() string { return "delivery_plans" }
