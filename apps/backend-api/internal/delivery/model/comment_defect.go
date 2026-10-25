package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type CommentDefect struct {
	ID                guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID          guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID         guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID        guid.ID   `gorm:"column:work_item_id;not null"`
	ThreadID          guid.ID   `gorm:"column:thread_id;not null"`
	ExecutionID       guid.ID   `gorm:"column:execution_id;not null"`
	SourceFirstBody   string    `gorm:"column:source_first_body;not null"`
	FoundInWorkItemID guid.ID   `gorm:"column:found_in_work_item_id;not null"`
	AnchorSnapshot    string    `gorm:"column:anchor_snapshot;not null"`
	Steps             string    `gorm:"column:steps;not null"`
	Expected          string    `gorm:"column:expected;not null"`
	Actual            string    `gorm:"column:actual;not null"`
	FoundStage        string    `gorm:"column:found_stage;size:16;not null"`
	CreatedBy         guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (CommentDefect) TableName() string { return "delivery_comment_defects" }
