package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ChangeRecord is the immutable decision and version boundary of a scope change.
type ChangeRecord struct {
	TenantID        guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	RequirementID   guid.ID   `gorm:"column:requirement_id;not null"`
	Kind            string    `gorm:"column:kind;not null"`
	FromVersionNo   int       `gorm:"column:from_version_no;not null"`
	ToVersionNo     int       `gorm:"column:to_version_no;not null"`
	Summary         string    `gorm:"column:summary;not null"`
	Diff            string    `gorm:"column:diff;not null"`
	AuthoritySource string    `gorm:"column:authority_source;not null"`
	ActorID         guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ChangeRecord) TableName() string { return "delivery_change_records" }

// ChangeImpact freezes which exact work version was made stale by a change.
type ChangeImpact struct {
	TenantID       guid.ID `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ChangeRecordID guid.ID `gorm:"column:change_record_id;primaryKey;autoIncrement:false;not null"`
	WorkItemID     guid.ID `gorm:"column:work_item_id;primaryKey;autoIncrement:false;not null"`
	ProjectID      guid.ID `gorm:"column:project_id;not null"`
	Impact         string  `gorm:"column:impact;not null"`
	VersionBefore  int     `gorm:"column:version_before;not null"`
}

func (ChangeImpact) TableName() string { return "delivery_change_impacts" }

// ResultFeedback anchors a comment to an immutable Human result, never a mutable
// work item head or an invented execution. Internal work identifiers stay server-side.
type ResultFeedback struct {
	CommentThreadID *guid.ID  `gorm:"column:comment_thread_id"`
	AnchorSnapshot  *string   `gorm:"column:anchor_snapshot;type:text"`
	Location        string    `gorm:"column:location;not null;default:''"`
	Expected        string    `gorm:"column:expected;not null;default:''"`
	TenantID        guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID      guid.ID   `gorm:"column:work_item_id;not null"`
	ResultVersionID guid.ID   `gorm:"column:result_version_id;not null"`
	Kind            string    `gorm:"column:kind;not null"`
	Category        string    `gorm:"column:category;not null"`
	Detail          string    `gorm:"column:detail;not null"`
	ActorID         guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ResultFeedback) TableName() string { return "delivery_result_feedback" }
