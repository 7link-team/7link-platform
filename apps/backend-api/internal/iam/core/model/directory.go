package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type TenantMember struct {
	TenantID    guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	PrincipalID guid.ID    `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	DisplayName string     `gorm:"column:display_name;size:128;not null"`
	Email       string     `gorm:"column:email;size:320;not null"`
	Status      string     `gorm:"column:status;size:16;not null"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null"`
	DisabledAt  *time.Time `gorm:"column:disabled_at"`
}

func (TenantMember) TableName() string { return "iam_tenant_members" }

// MembershipPeriod is an observed active interval. A migrated interval starts
// at observation time; older membership transitions cannot be reconstructed.
type MembershipPeriod struct {
	TenantID    guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	PrincipalID guid.ID    `gorm:"column:principal_id;not null"`
	StartedAt   time.Time  `gorm:"column:started_at;not null"`
	EndedAt     *time.Time `gorm:"column:ended_at"`
}

func (MembershipPeriod) TableName() string { return "iam_membership_periods" }

type Menu struct {
	App            string    `gorm:"column:app;size:32;not null;default:iam"`
	Domain         string    `gorm:"column:domain;size:16;not null;default:''"`
	Space          string    `gorm:"column:space;size:16;not null;default:''"`
	TenantID       guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID             guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	ParentID       *guid.ID  `gorm:"column:parent_id"`
	Label          string    `gorm:"column:label;size:128;not null"`
	Translations   string    `gorm:"column:translations;type:text;not null"`
	Route          *string   `gorm:"column:route;size:512"`
	Icon           string    `gorm:"column:icon;size:64;not null"`
	SortOrder      int       `gorm:"column:sort_order;not null"`
	PermissionCode string    `gorm:"column:permission_code;size:128;not null"`
	Status         string    `gorm:"column:status;size:16;not null"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (Menu) TableName() string { return "iam_menus" }
