// Package model contains provisioning's persisted row types.
package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type Directory struct {
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID  guid.ID   `gorm:"column:tenant_id;not null"`
	Name      string    `gorm:"column:name;size:128;not null"`
	NameKey   string    `gorm:"column:name_key;size:128;not null"`
	Status    string    `gorm:"column:status;size:16;not null"`
	Version   int64     `gorm:"column:version;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (Directory) TableName() string { return "iam_scim_directories" }

type Credential struct {
	ID          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	DirectoryID guid.ID    `gorm:"column:directory_id;not null"`
	Name        string     `gorm:"column:name;size:128;not null"`
	TokenHash   string     `gorm:"column:token_hash;type:text;not null"`
	ExpiresAt   *time.Time `gorm:"column:expires_at"`
	LastUsedAt  *time.Time `gorm:"column:last_used_at"`
	RevokedAt   *time.Time `gorm:"column:revoked_at"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
}

func (Credential) TableName() string { return "iam_scim_credentials" }

type Resource struct {
	DirectoryID  guid.ID    `gorm:"column:directory_id;primaryKey;autoIncrement:false"`
	ResourceType string     `gorm:"column:resource_type;primaryKey;size:16;autoIncrement:false"`
	ResourceID   guid.ID    `gorm:"column:resource_id;primaryKey;autoIncrement:false"`
	ExternalID   string     `gorm:"column:external_id;size:512;not null"`
	ExternalKey  string     `gorm:"column:external_key;size:512;not null"`
	Version      int64      `gorm:"column:version;not null"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
}

func (Resource) TableName() string { return "iam_scim_resources" }

type Target struct {
	ID                    guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID              guid.ID   `gorm:"column:tenant_id;not null"`
	Name                  string    `gorm:"column:name;size:128;not null"`
	NameKey               string    `gorm:"column:name_key;size:128;not null"`
	BaseURL               string    `gorm:"column:base_url;size:1024;not null"`
	BearerTokenCiphertext string    `gorm:"column:bearer_token_ciphertext;type:text;not null"`
	Status                string    `gorm:"column:status;size:16;not null"`
	Version               int64     `gorm:"column:version;not null"`
	CreatedAt             time.Time `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time `gorm:"column:updated_at;not null"`
}

func (Target) TableName() string { return "iam_scim_targets" }

type TargetResource struct {
	TargetID     guid.ID    `gorm:"column:target_id;primaryKey;autoIncrement:false"`
	ResourceType string     `gorm:"column:resource_type;primaryKey;size:16;autoIncrement:false"`
	ResourceID   guid.ID    `gorm:"column:resource_id;primaryKey;autoIncrement:false"`
	ExternalID   string     `gorm:"column:external_id;size:512;not null"`
	Version      int64      `gorm:"column:version;not null"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
}

func (TargetResource) TableName() string { return "iam_scim_target_resources" }
