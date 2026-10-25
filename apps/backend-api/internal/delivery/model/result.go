package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ResultVersion is an immutable Human completion claim. A manual submission
// has no Execution: submitting it never invents a runner or verification fact.
type ResultVersion struct {
	VerificationPlan  string    `gorm:"column:verification_plan;not null"`
	SourceExecutionID *guid.ID  `gorm:"column:source_execution_id"`
	HeadCommit        string    `gorm:"column:head_commit;not null"`
	TenantID          guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID         guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID        guid.ID   `gorm:"column:work_item_id;not null"`
	Sequence          int       `gorm:"column:sequence;not null"`
	Summary           string    `gorm:"column:summary;not null"`
	ActorID           guid.ID   `gorm:"column:actor_id;not null"`
	BaselineVersionNo int       `gorm:"column:baseline_version_no;not null"`
	PreviousResultID  *guid.ID  `gorm:"column:previous_result_id"`
	ReworkActionID    *guid.ID  `gorm:"column:rework_action_id"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ResultVersion) TableName() string { return "delivery_result_versions" }

// ResultEvidence pins a file version and its criterion mapping at submission.
type ResultEvidence struct {
	TenantID    guid.ID  `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID          guid.ID  `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID   guid.ID  `gorm:"column:project_id;not null"`
	WorkItemID  guid.ID  `gorm:"column:work_item_id;not null"`
	ResultID    guid.ID  `gorm:"column:result_id;not null"`
	BindingID   guid.ID  `gorm:"column:binding_id;not null"`
	SpaceID     guid.ID  `gorm:"column:space_id;not null"`
	FileID      guid.ID  `gorm:"column:file_id;not null"`
	VersionID   guid.ID  `gorm:"column:version_id;not null"`
	CriterionID *guid.ID `gorm:"column:criterion_id"`
	Coverage    string   `gorm:"column:coverage;not null"`
	Note        string   `gorm:"column:note;not null"`
	Name        string   `gorm:"column:name;not null"`
	SHA256      string   `gorm:"column:sha256;not null"`
}

func (ResultEvidence) TableName() string { return "delivery_result_evidence" }

// ResultAction preserves independent manual checks and exact-version decisions.
type ResultAction struct {
	AttentionMS     *int64    `gorm:"column:attention_ms"`
	TenantID        guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID      guid.ID   `gorm:"column:work_item_id;not null"`
	ResultID        guid.ID   `gorm:"column:result_id;not null"`
	Kind            string    `gorm:"column:kind;not null"`
	Verdict         string    `gorm:"column:verdict;not null"`
	ActorID         guid.ID   `gorm:"column:actor_id;not null"`
	AuthoritySource string    `gorm:"column:authority_source;not null"`
	Reason          string    `gorm:"column:reason;not null"`
	HandoffID       *guid.ID  `gorm:"column:handoff_id"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ResultAction) TableName() string { return "delivery_result_actions" }

// ResultHandoff freezes a manifest and the exact accepted result being handed
// over. Creating a package does not assert that an external send occurred.
type ResultHandoff struct {
	TenantID   guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID         guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID  guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID guid.ID   `gorm:"column:work_item_id;not null"`
	ResultID   guid.ID   `gorm:"column:result_id;not null"`
	DeliveryID guid.ID   `gorm:"column:delivery_id;not null"`
	Summary    string    `gorm:"column:summary;not null"`
	Manifest   string    `gorm:"column:manifest;not null"`
	SHA256     string    `gorm:"column:sha256;not null"`
	ActorID    guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ResultHandoff) TableName() string { return "delivery_result_handoffs" }
