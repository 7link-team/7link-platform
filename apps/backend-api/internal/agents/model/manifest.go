package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ManifestVersion is an immutable system-owned publication, never tenant data.
type ManifestVersion struct {
	ID                guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID          guid.ID   `gorm:"column:tenant_id;not null"`
	ManifestVersion   string    `gorm:"column:manifest_version;not null"`
	SHA256            string    `gorm:"column:sha256;not null"`
	Document          string    `gorm:"column:document;not null"`
	OperationCount    int       `gorm:"column:operation_count;not null"`
	SupersedesVersion string    `gorm:"column:supersedes_version;not null"`
	PublishedAt       time.Time `gorm:"column:published_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (ManifestVersion) TableName() string { return "agents_operation_manifest_versions" }
