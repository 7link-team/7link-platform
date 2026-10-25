package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ToolVersion preserves published configuration. Only lifecycle state may change.
type ToolVersion struct {
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	ToolID      guid.ID   `gorm:"column:tool_id;not null"`
	VersionNo   int       `gorm:"column:version_no;not null"`
	Transport   string    `gorm:"column:transport;not null"`
	Endpoint    string    `gorm:"column:endpoint;not null"`
	ContentHash string    `gorm:"column:content_hash;not null"`
	SkillBody   string    `gorm:"column:skill_body;not null"`
	State       string    `gorm:"column:state;not null"`
	Source      string    `gorm:"column:source;not null"`
	SourceRef   string    `gorm:"column:source_ref;not null"`
	ChangeNote  string    `gorm:"column:change_note;not null"`
	PublishedBy guid.ID   `gorm:"column:published_by;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (ToolVersion) TableName() string { return "agents_tool_versions" }

// ToolUpstream records a human-reported upstream change without replacing approval.
type ToolUpstream struct {
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	ToolID      guid.ID   `gorm:"column:tool_id;not null"`
	ContentHash string    `gorm:"column:content_hash;not null"`
	Note        string    `gorm:"column:note;not null"`
	ReportedBy  guid.ID   `gorm:"column:reported_by;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (ToolUpstream) TableName() string { return "agents_tool_upstreams" }
