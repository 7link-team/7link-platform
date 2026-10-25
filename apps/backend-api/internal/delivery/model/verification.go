package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// VerificationRun is an exact-result attempt. A completed verdict is immutable;
// retries append another attempt rather than changing the original conclusion.
type VerificationRun struct {
	ReviewerID     *guid.ID   `gorm:"column:reviewer_id"`
	ReviewerModel  string     `gorm:"column:reviewer_model;not null"`
	ReviewerFamily string     `gorm:"column:reviewer_family;not null"`
	FamilyFallback bool       `gorm:"column:family_fallback;not null"`
	TenantID       guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID             guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID      guid.ID    `gorm:"column:project_id;not null"`
	WorkItemID     guid.ID    `gorm:"column:work_item_id;not null"`
	ResultID       guid.ID    `gorm:"column:result_id;not null"`
	CriterionID    guid.ID    `gorm:"column:criterion_id;not null"`
	Method         string     `gorm:"column:method;not null"`
	Stage          string     `gorm:"column:stage;not null"`
	Round          int        `gorm:"column:round;not null"`
	State          string     `gorm:"column:state;not null"`
	Verdict        string     `gorm:"column:verdict;not null"`
	Reason         string     `gorm:"column:reason;not null"`
	ActorID        guid.ID    `gorm:"column:actor_id;not null"`
	TaskID         *guid.ID   `gorm:"column:task_id"`
	CheckID        *guid.ID   `gorm:"column:check_id"`
	ReceiptHash    string     `gorm:"column:receipt_hash;not null"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	CompletedAt    *time.Time `gorm:"column:completed_at"`
}

func (VerificationRun) TableName() string { return "delivery_verification_runs" }

// VerificationOverride records a separate Human exception. It never changes a
// VerificationRun verdict and is valid only for this exact evidence/baseline.
type VerificationOverride struct {
	TenantID             guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                   guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID            guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID           guid.ID   `gorm:"column:work_item_id;not null"`
	ResultID             guid.ID   `gorm:"column:result_id;not null"`
	BaselineVersionNo    int       `gorm:"column:baseline_version_no;not null"`
	ArtifactHash         string    `gorm:"column:artifact_hash;not null"`
	Verdicts             string    `gorm:"column:verdicts;not null"`
	Reason               string    `gorm:"column:reason;not null"`
	Risk                 string    `gorm:"column:risk;not null"`
	EvidenceIDs          string    `gorm:"column:evidence_ids;not null"`
	ActorID              guid.ID   `gorm:"column:actor_id;not null"`
	AuthoritySource      string    `gorm:"column:authority_source;not null"`
	ReauthenticationHash string    `gorm:"column:reauthentication_hash;not null"`
	CreatedAt            time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (VerificationOverride) TableName() string { return "delivery_verification_overrides" }
