// Package model contains the core IAM module's persisted row types.
package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type Role struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	Name        string    `gorm:"column:name;size:128;not null"`
	NameKey     string    `gorm:"column:name_key;size:128;not null"`
	Description string    `gorm:"column:description;type:text;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null"`
}

func (Role) TableName() string { return "iam_roles" }

type RolePermission struct {
	TenantID       guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	RoleID         guid.ID   `gorm:"column:role_id;primaryKey;autoIncrement:false"`
	PermissionCode string    `gorm:"column:permission_code;primaryKey;size:128"`
	ConditionJSON  string    `gorm:"column:condition_json;type:text;not null"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
}

func (RolePermission) TableName() string { return "iam_role_permissions" }

type RoleMember struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	RoleID      guid.ID   `gorm:"column:role_id;primaryKey;autoIncrement:false"`
	PrincipalID guid.ID   `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
}

func (RoleMember) TableName() string { return "iam_role_members" }

type RoleBinding struct {
	TenantID    guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	RoleID      guid.ID    `gorm:"column:role_id;primaryKey;autoIncrement:false"`
	PrincipalID guid.ID    `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	ScopeType   string     `gorm:"column:scope_type;primaryKey;size:32"`
	ScopeID     guid.ID    `gorm:"column:scope_id;primaryKey;autoIncrement:false"`
	Effect      string     `gorm:"column:effect;size:8;not null"`
	ExpiresAt   *time.Time `gorm:"column:expires_at"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
}

func (RoleBinding) TableName() string { return "iam_role_bindings" }

type TemporaryRoleGrant struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	RoleID      guid.ID   `gorm:"column:role_id;not null"`
	PrincipalID guid.ID   `gorm:"column:principal_id;not null"`
	SourceType  string    `gorm:"column:source_type;size:32;not null"`
	SourceID    guid.ID   `gorm:"column:source_id;not null"`
	StartsAt    time.Time `gorm:"column:starts_at;not null"`
	EndsAt      time.Time `gorm:"column:ends_at;not null"`
	CreatedBy   guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
}

func (TemporaryRoleGrant) TableName() string { return "iam_temporary_role_grants" }
