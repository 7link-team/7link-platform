package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type ArtifactCommentThread struct {
	AnchorCheckedVersion *guid.ID  `gorm:"column:anchor_checked_version"`
	RelocatedAnchor      *string   `gorm:"column:relocated_anchor;type:text"`
	AnchorStatus         string    `gorm:"column:anchor_status;size:16;not null;default:CURRENT"`
	ID                   guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID             guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID            guid.ID   `gorm:"column:project_id;not null"`
	ExecutionID          guid.ID   `gorm:"column:execution_id;not null"`
	WorkItemID           guid.ID   `gorm:"column:work_item_id;not null"`
	EvidenceID           guid.ID   `gorm:"column:evidence_id;not null"`
	Visibility           string    `gorm:"column:visibility;size:16;not null"`
	State                string    `gorm:"column:state;size:16;not null"`
	AnchorSnapshot       string    `gorm:"column:anchor_snapshot;not null"`
	FirstBody            string    `gorm:"column:first_body;not null"`
	CreatedBy            guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt            time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	Version              int       `gorm:"column:version;not null"`
}

func (ArtifactCommentThread) TableName() string { return "execution_artifact_comment_threads" }

type ArtifactCommentMessage struct {
	EditCount   int        `gorm:"column:edit_count;not null;default:0"`
	EditedAt    *time.Time `gorm:"column:edited_at"`
	RetractedAt *time.Time `gorm:"column:retracted_at"`
	ActorKind   string     `gorm:"column:actor_kind;size:16;not null;default:PRINCIPAL"`
	Seq         int64      `gorm:"column:seq;not null;default:1"`
	ID          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID    guid.ID    `gorm:"column:tenant_id;not null"`
	ThreadID    guid.ID    `gorm:"column:thread_id;not null"`
	Body        string     `gorm:"column:body;not null"`
	CreatedBy   guid.ID    `gorm:"column:created_by;not null"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ArtifactCommentMessage) TableName() string { return "execution_artifact_comment_messages" }

type ArtifactCommentConversion struct {
	ActorKind  string    `gorm:"column:actor_kind;size:16;not null;default:PRINCIPAL"`
	Seq        int64     `gorm:"column:seq;not null;default:2"`
	ID         guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID   guid.ID   `gorm:"column:tenant_id;not null"`
	ThreadID   guid.ID   `gorm:"column:thread_id;not null"`
	TargetKind string    `gorm:"column:target_kind;size:16;not null"`
	TargetID   guid.ID   `gorm:"column:target_id;not null"`
	ActorID    guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ArtifactCommentConversion) TableName() string { return "execution_artifact_comment_conversions" }

// ArtifactCommentEvent preserves thread chronology independently of mutable
// message presentation. No body or source excerpt is copied into an event.
type ArtifactCommentEvent struct {
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID  guid.ID   `gorm:"column:tenant_id;not null"`
	ThreadID  guid.ID   `gorm:"column:thread_id;not null"`
	Seq       int64     `gorm:"column:seq;not null"`
	Kind      string    `gorm:"column:kind;size:32;not null"`
	ActorID   guid.ID   `gorm:"column:actor_id;not null"`
	ActorKind string    `gorm:"column:actor_kind;size:16;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ArtifactCommentEvent) TableName() string { return "execution_artifact_comment_events" }
