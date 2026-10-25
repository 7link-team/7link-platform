package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// CodeLink is append-only provenance. A recorded reference never asserts that
// a merge, deployment or verification happened.
type CodeLink struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null" json:"-"`
	ProjectID    guid.ID   `gorm:"column:project_id;not null" json:"project_id"`
	TargetKind   string    `gorm:"column:target_kind;size:32;not null" json:"target_kind"`
	TargetID     guid.ID   `gorm:"column:target_id;not null" json:"target_id"`
	Kind         string    `gorm:"column:kind;size:32;not null" json:"kind"`
	RepositoryID guid.ID   `gorm:"column:repository_id;not null" json:"repository_id"`
	Repository   string    `gorm:"column:repository;size:300;not null" json:"repository"`
	Ref          string    `gorm:"column:ref;size:200;not null" json:"ref"`
	Title        string    `gorm:"column:title;size:300;not null" json:"title"`
	URL          string    `gorm:"column:url;size:500;not null" json:"url"`
	Source       string    `gorm:"column:source;size:16;not null" json:"source"`
	Provenance   string    `gorm:"column:provenance;size:32;not null" json:"provenance"`
	Snapshot     string    `gorm:"column:snapshot;not null" json:"snapshot"`
	ContentHash  string    `gorm:"column:content_hash;size:64;not null" json:"content_sha256"`
	CreatedBy    guid.ID   `gorm:"column:created_by;not null" json:"created_by"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
}

func (CodeLink) TableName() string { return "delivery_code_links" }

// CodeLinkScan logs every unresolved project-local token without creating an
// association to a work item from another project.
type CodeLinkScan struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null" json:"-"`
	ProjectID    guid.ID   `gorm:"column:project_id;not null" json:"project_id"`
	RepositoryID guid.ID   `gorm:"column:repository_id;not null" json:"repository_id"`
	Ref          string    `gorm:"column:ref;size:200;not null" json:"ref"`
	Token        string    `gorm:"column:token;size:40;not null" json:"token"`
	Reason       string    `gorm:"column:reason;size:32;not null" json:"reason"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
}

func (CodeLinkScan) TableName() string { return "delivery_code_link_scans" }
