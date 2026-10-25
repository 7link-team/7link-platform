package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ReviewSheet persists a frozen requirement or work-item review and latest opinions.
type ReviewSheet struct {
	ID                guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID          guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID         guid.ID   `gorm:"column:project_id;not null"`
	RequirementID     guid.ID   `gorm:"column:requirement_id;not null"`
	WorkItemID        *guid.ID  `gorm:"column:work_item_id"`
	OpenWorkItemID    *guid.ID  `gorm:"column:open_work_item_id"`
	OpenRequirementID *guid.ID  `gorm:"column:open_requirement_id"`
	InitiatorID       guid.ID   `gorm:"column:initiator_id;not null"`
	Document          string    `gorm:"column:document;not null"`
	State             string    `gorm:"column:state;not null"`
	Version           int       `gorm:"column:version;not null"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt         time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (ReviewSheet) TableName() string { return "delivery_review_sheets" }
