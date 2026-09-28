// Package model contains the organization module's persisted row types.
package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type Department struct {
	TenantID  guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	EntityID  *guid.ID  `gorm:"column:entity_id"`
	ParentID  *guid.ID  `gorm:"column:parent_id"`
	Name      string    `gorm:"column:name;size:128;not null"`
	NameKey   string    `gorm:"column:name_key;size:128;not null"`
	Status    string    `gorm:"column:status;size:16;not null"`
	SortOrder int       `gorm:"column:sort_order;not null"`
	Version   int64     `gorm:"column:version;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (Department) TableName() string { return "iam_departments" }

type DepartmentClosure struct {
	TenantID     guid.ID `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	AncestorID   guid.ID `gorm:"column:ancestor_id;primaryKey;autoIncrement:false"`
	DescendantID guid.ID `gorm:"column:descendant_id;primaryKey;autoIncrement:false"`
	Depth        int     `gorm:"column:depth;not null"`
}

func (DepartmentClosure) TableName() string { return "iam_department_closure" }

type Position struct {
	TenantID  guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	Code      string    `gorm:"column:code;size:64;not null"`
	Name      string    `gorm:"column:name;size:128;not null"`
	Status    string    `gorm:"column:status;size:16;not null"`
	SortOrder int       `gorm:"column:sort_order;not null"`
	Version   int64     `gorm:"column:version;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (Position) TableName() string { return "iam_positions" }

type PositionMember struct {
	TenantID    guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	PositionID  guid.ID    `gorm:"column:position_id;primaryKey;autoIncrement:false"`
	PrincipalID guid.ID    `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	StartsAt    *time.Time `gorm:"column:starts_at"`
	EndsAt      *time.Time `gorm:"column:ends_at"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null"`
}

func (PositionMember) TableName() string { return "iam_position_members" }

type Group struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	Name        string    `gorm:"column:name;size:128;not null"`
	NameKey     string    `gorm:"column:name_key;size:128;not null"`
	GroupType   string    `gorm:"column:group_type;size:32;not null"`
	RuleJSON    string    `gorm:"column:rule_json;type:text;not null"`
	Description string    `gorm:"column:description;type:text;not null"`
	Status      string    `gorm:"column:status;size:32;not null"`
	SortOrder   int       `gorm:"column:sort_order;not null"`
	Version     int64     `gorm:"column:version;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null"`
}

func (Group) TableName() string { return "iam_groups" }

type GroupMember struct {
	TenantID    guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	GroupID     guid.ID    `gorm:"column:group_id;primaryKey;autoIncrement:false"`
	PrincipalID guid.ID    `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	StartsAt    *time.Time `gorm:"column:starts_at"`
	EndsAt      *time.Time `gorm:"column:ends_at"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null"`
}

func (GroupMember) TableName() string { return "iam_group_members" }

type GroupRoleBinding struct {
	TenantID  guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	RoleID    guid.ID   `gorm:"column:role_id;primaryKey;autoIncrement:false"`
	GroupID   guid.ID   `gorm:"column:group_id;primaryKey;autoIncrement:false"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (GroupRoleBinding) TableName() string { return "iam_group_role_bindings" }

type PositionRoleBinding struct {
	TenantID   guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	RoleID     guid.ID   `gorm:"column:role_id;primaryKey;autoIncrement:false"`
	PositionID guid.ID   `gorm:"column:position_id;primaryKey;autoIncrement:false"`
	CreatedAt  time.Time `gorm:"column:created_at;not null"`
}

func (PositionRoleBinding) TableName() string { return "iam_position_role_bindings" }

type Tenant struct {
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	ScopeKind string    `gorm:"column:scope_kind;size:16;not null;default:tenant"`
	Slug      string    `gorm:"column:slug;size:63;not null"`
	Name      string    `gorm:"column:name;size:128;not null"`
	Status    string    `gorm:"column:status;size:16;not null"`
	Version   int64     `gorm:"column:version;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (Tenant) TableName() string { return "iam_tenants" }

type MemberDepartment struct {
	TenantID     guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	PrincipalID  guid.ID   `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	DepartmentID guid.ID   `gorm:"column:department_id;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

func (MemberDepartment) TableName() string { return "iam_member_departments" }

type RoleDataScope struct {
	TenantID  guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	RoleID    guid.ID   `gorm:"column:role_id;primaryKey;autoIncrement:false"`
	ScopeType string    `gorm:"column:scope_type;size:32;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (RoleDataScope) TableName() string { return "iam_role_data_scopes" }

type RoleScopeDepartment struct {
	TenantID     guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	RoleID       guid.ID   `gorm:"column:role_id;primaryKey;autoIncrement:false"`
	DepartmentID guid.ID   `gorm:"column:department_id;primaryKey;autoIncrement:false"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
}

func (RoleScopeDepartment) TableName() string { return "iam_role_scope_departments" }

type Invitation struct {
	TenantID     guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID           guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	Email        string     `gorm:"column:email;size:320;not null"`
	EmailKey     string     `gorm:"column:email_key;size:320;not null"`
	TokenHMAC    string     `gorm:"column:token_hmac;size:64;not null"`
	Status       string     `gorm:"column:status;size:16;not null"`
	DepartmentID *guid.ID   `gorm:"column:department_id"`
	ExpiresAt    time.Time  `gorm:"column:expires_at;not null"`
	AcceptedBy   *guid.ID   `gorm:"column:accepted_by"`
	AcceptedAt   *time.Time `gorm:"column:accepted_at"`
	RevokedAt    *time.Time `gorm:"column:revoked_at"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null"`
}

func (Invitation) TableName() string { return "iam_invitations" }

type InvitationRole struct {
	TenantID     guid.ID `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	InvitationID guid.ID `gorm:"column:invitation_id;primaryKey;autoIncrement:false"`
	RoleID       guid.ID `gorm:"column:role_id;primaryKey;autoIncrement:false"`
}

func (InvitationRole) TableName() string { return "iam_invitation_roles" }
