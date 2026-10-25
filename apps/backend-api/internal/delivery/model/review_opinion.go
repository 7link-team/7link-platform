package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ReviewOpinion is an append-only submission; supersession never changes a prior row.
type ReviewOpinion struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID    guid.ID   `gorm:"column:project_id;not null"`
	SheetID      guid.ID   `gorm:"column:sheet_id;not null"`
	ReviewerID   guid.ID   `gorm:"column:reviewer_id;not null"`
	SupersedesID *guid.ID  `gorm:"column:supersedes_id"`
	Document     string    `gorm:"column:document;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ReviewOpinion) TableName() string { return "delivery_review_opinions" }
