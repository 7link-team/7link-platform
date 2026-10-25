package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Skill struct {
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	TenantID         guid.ID   `gorm:"column:tenant_id;not null" json:"-"`
	SkillKey         string    `gorm:"column:skill_key;size:64;not null" json:"skill_key"`
	Title            string    `gorm:"column:title;size:200;not null" json:"title"`
	Description      string    `gorm:"column:description;size:1024;not null" json:"description"`
	State            string    `gorm:"column:state;not null" json:"state"`
	CurrentVersionID *guid.ID  `gorm:"column:current_version_id" json:"current_version_id,omitempty"`
	Version          int       `gorm:"column:version;not null" json:"version"`
	CreatedBy        guid.ID   `gorm:"column:created_by;not null" json:"created_by"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false" json:"updated_at"`
}

func (Skill) TableName() string { return "knowledge_skills" }

type SkillVersion struct {
	ID                guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	TenantID          guid.ID    `gorm:"column:tenant_id;not null" json:"-"`
	SkillID           guid.ID    `gorm:"column:skill_id;not null" json:"skill_id"`
	VersionNo         int        `gorm:"column:version_no;not null" json:"version_no"`
	Content           string     `gorm:"column:content;not null" json:"content,omitempty"`
	Frontmatter       string     `gorm:"column:frontmatter;not null" json:"frontmatter"`
	ContentHash       string     `gorm:"column:content_hash;size:64;not null" json:"content_hash"`
	State             string     `gorm:"column:state;not null" json:"state"`
	Origin            string     `gorm:"column:origin;not null" json:"origin"`
	SourceWorkItemID  *guid.ID   `gorm:"column:source_work_item_id" json:"source_work_item_id,omitempty"`
	SourceExecutionID *guid.ID   `gorm:"column:source_execution_id" json:"source_execution_id,omitempty"`
	ReviewedBy        *guid.ID   `gorm:"column:reviewed_by" json:"reviewed_by,omitempty"`
	ReviewedAt        *time.Time `gorm:"column:reviewed_at" json:"reviewed_at,omitempty"`
	ReviewReason      string     `gorm:"column:review_reason;size:2000;not null" json:"review_reason"`
	CreatedBy         guid.ID    `gorm:"column:created_by;not null" json:"created_by"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
}

func (SkillVersion) TableName() string { return "knowledge_skill_versions" }

type AgentSkill struct {
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	TenantID        guid.ID   `gorm:"column:tenant_id;not null" json:"-"`
	AgentID         guid.ID   `gorm:"column:agent_id;not null" json:"agent_id"`
	SkillID         guid.ID   `gorm:"column:skill_id;not null" json:"skill_id"`
	PinnedVersionID *guid.ID  `gorm:"column:pinned_version_id" json:"pinned_version_id,omitempty"`
	AttachedBy      guid.ID   `gorm:"column:attached_by;not null" json:"attached_by"`
	AttachedAt      time.Time `gorm:"column:attached_at;not null" json:"attached_at"`
	LiveSlot        *int      `gorm:"column:live_slot" json:"-"`
}

func (AgentSkill) TableName() string { return "knowledge_agent_skills" }

// Distillation is an actual runtime request, not a skill version. A draft is
// created only after the execution owner verifies persisted model output.
type Distillation struct {
	ID                guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"distillation_id"`
	TenantID          guid.ID   `gorm:"column:tenant_id;not null" json:"-"`
	ProjectID         guid.ID   `gorm:"column:project_id;not null" json:"project_id"`
	WorkItemID        guid.ID   `gorm:"column:work_item_id;not null" json:"work_item_id"`
	AgentPrincipalID  guid.ID   `gorm:"column:agent_principal_id;not null" json:"agent_id"`
	SourceExecutionID guid.ID   `gorm:"column:source_execution_id;not null" json:"source_execution_id"`
	SourceResultID    guid.ID   `gorm:"column:source_result_id;not null" json:"source_result_id"`
	InputHash         string    `gorm:"column:input_hash;size:64;not null" json:"input_hash"`
	ExecutionID       *guid.ID  `gorm:"column:execution_id" json:"execution_id,omitempty"`
	SkillVersionID    *guid.ID  `gorm:"column:skill_version_id" json:"skill_version_id,omitempty"`
	State             string    `gorm:"column:state;size:32;not null" json:"state"`
	CreatedBy         guid.ID   `gorm:"column:created_by;not null" json:"created_by"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
}

func (Distillation) TableName() string { return "knowledge_skill_distillations" }
