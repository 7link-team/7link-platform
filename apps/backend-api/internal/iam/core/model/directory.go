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

type Menu struct {
	TenantID       guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID             guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	ParentID       *guid.ID  `gorm:"column:parent_id"`
	Label          string    `gorm:"column:label;size:128;not null"`
	Route          *string   `gorm:"column:route;size:512"`
	Icon           string    `gorm:"column:icon;size:64;not null"`
	SortOrder      int       `gorm:"column:sort_order;not null"`
	PermissionCode string    `gorm:"column:permission_code;size:128;not null"`
	Status         string    `gorm:"column:status;size:16;not null"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (Menu) TableName() string { return "iam_menus" }
