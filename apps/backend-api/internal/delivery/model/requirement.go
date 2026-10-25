package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// ProjectCounter is owned by delivery (spec 02).
type ProjectCounter struct {
	TenantID          guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID         guid.ID   `gorm:"column:project_id;not null"`
	NextRequirementNo int       `gorm:"column:next_requirement_no;not null"`
	NextWorkItemNo    int       `gorm:"column:next_work_item_no;not null"`
	UpdatedAt         time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	CreatedBy         *guid.ID  `gorm:"column:created_by"`
}

func (ProjectCounter) TableName() string { return "delivery_project_counters" }

// Requirement is owned by delivery (spec 02).
type Requirement struct {
	ReviewState      string     `gorm:"column:review_state;not null;default:NEEDS_WORK"`
	TenantID         guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID               guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID        guid.ID    `gorm:"column:project_id;not null"`
	Number           int        `gorm:"column:number;not null"`
	Title            string     `gorm:"column:title;not null"`
	Description      string     `gorm:"column:description;not null"`
	Priority         int        `gorm:"column:priority;not null"`
	Status           string     `gorm:"column:status;not null"`
	CurrentVersionNo int        `gorm:"column:current_version_no;not null"`
	SourceIntakeID   *guid.ID   `gorm:"column:source_intake_id"`
	ArchivedAt       *time.Time `gorm:"column:archived_at"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	CreatedBy        *guid.ID   `gorm:"column:created_by"`
	UpdatedAt        time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	UpdatedBy        *guid.ID   `gorm:"column:updated_by"`
	Version          int        `gorm:"column:version;not null"`
}

func (Requirement) TableName() string { return "delivery_requirements" }

// AcceptanceCriterion is owned by delivery (spec 02).
type AcceptanceCriterion struct {
	TenantID            guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                  guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID           guid.ID    `gorm:"column:project_id;not null"`
	RequirementID       guid.ID    `gorm:"column:requirement_id;not null"`
	Code                string     `gorm:"column:code;not null"`
	Text                string     `gorm:"column:text;not null"`
	Required            bool       `gorm:"column:required;not null"`
	Position            int        `gorm:"column:position;not null"`
	IntroducedInVersion int        `gorm:"column:introduced_in_version;not null"`
	RetiredInVersion    *int       `gorm:"column:retired_in_version"`
	SupersedesID        *guid.ID   `gorm:"column:supersedes_id"`
	ArchivedAt          *time.Time `gorm:"column:archived_at"`
	CreatedAt           time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	CreatedBy           *guid.ID   `gorm:"column:created_by"`
}

func (AcceptanceCriterion) TableName() string { return "delivery_acceptance_criteria" }

// RequirementVersion is owned by delivery (spec 02).
type RequirementVersion struct {
	AcceptanceSuite string    `gorm:"column:acceptance_suite;not null;default:{}"`
	SuiteHash       string    `gorm:"column:suite_hash;not null;default:''"`
	TenantID        guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	RequirementID   guid.ID   `gorm:"column:requirement_id;not null"`
	VersionNo       int       `gorm:"column:version_no;not null"`
	Title           string    `gorm:"column:title;not null"`
	Description     string    `gorm:"column:description;not null"`
	Priority        int       `gorm:"column:priority;not null"`
	Criteria        string    `gorm:"column:criteria;not null"`
	ChangeRecordID  *guid.ID  `gorm:"column:change_record_id"`
	ContentHash     string    `gorm:"column:content_hash;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	CreatedBy       *guid.ID  `gorm:"column:created_by"`
}

func (RequirementVersion) TableName() string { return "delivery_requirement_versions" }
