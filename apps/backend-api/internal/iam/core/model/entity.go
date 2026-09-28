package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type Entity struct {
	TenantID  guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	ParentID  *guid.ID  `gorm:"column:parent_id"`
	Type      string    `gorm:"column:type;size:64;not null"`
	Name      string    `gorm:"column:name;size:200;not null"`
	NameKey   string    `gorm:"column:name_key;size:200;not null"`
	Status    string    `gorm:"column:status;size:16;not null"`
	Metadata  string    `gorm:"column:metadata;type:text;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (Entity) TableName() string { return "iam_entities" }

type PolicyRevision struct {
	TenantID  guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	Revision  int64     `gorm:"column:revision;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (PolicyRevision) TableName() string { return "iam_policy_revisions" }
