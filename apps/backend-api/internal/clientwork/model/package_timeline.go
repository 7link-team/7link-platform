package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type PackageTimeline struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID   guid.ID   `gorm:"column:project_id;not null"`
	PackageID   guid.ID   `gorm:"column:package_id;not null"`
	Kind        string    `gorm:"column:kind;not null;size:32"`
	State       string    `gorm:"column:state;not null;size:32"`
	Version     int       `gorm:"column:version;not null"`
	ContentHash string    `gorm:"column:content_hash;not null;size:64"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (PackageTimeline) TableName() string { return "clientwork_package_timeline" }
