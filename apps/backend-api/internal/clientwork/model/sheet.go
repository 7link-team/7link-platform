// Package model contains client collaboration persistence records.
package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Sheet struct {
	TenantID          guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID         guid.ID   `gorm:"column:project_id;not null"`
	Title             string    `gorm:"column:title;size:200;not null"`
	PublishedRevision int       `gorm:"column:published_revision;not null"`
	CurrentRevision   int       `gorm:"column:current_revision;not null"`
	State             string    `gorm:"column:state;size:32;not null"`
	Version           int       `gorm:"column:version;not null"`
	CreatedBy         guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt         time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Sheet) TableName() string { return "clientwork_sheets" }

type SheetRevision struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID   guid.ID   `gorm:"column:project_id;not null"`
	SheetID     guid.ID   `gorm:"column:sheet_id;not null"`
	Revision    int       `gorm:"column:revision;not null"`
	Content     string    `gorm:"column:content;not null"`
	ContentHash string    `gorm:"column:content_hash;size:64;not null"`
	SignerID    guid.ID   `gorm:"column:signer_id;not null"`
	CreatedBy   guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (SheetRevision) TableName() string { return "clientwork_sheet_revisions" }

type Signoff struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID   guid.ID   `gorm:"column:project_id;not null"`
	SheetID     guid.ID   `gorm:"column:sheet_id;not null"`
	RevisionID  guid.ID   `gorm:"column:revision_id;not null"`
	Decision    string    `gorm:"column:decision;size:32;not null"`
	Reason      string    `gorm:"column:reason;not null"`
	ContentHash string    `gorm:"column:content_hash;size:64;not null"`
	ActorID     guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Signoff) TableName() string { return "clientwork_signoffs" }
