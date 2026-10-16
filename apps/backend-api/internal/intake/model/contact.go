// Package model owns intake persistence records.
package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Contact records the source identity of an external party. It is not a login.
type Contact struct {
	ID                guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID          guid.ID    `gorm:"column:tenant_id;not null"`
	Kind              string     `gorm:"column:kind;size:32;not null"`
	DisplayName       string     `gorm:"column:display_name;size:120;not null"`
	Email             *string    `gorm:"column:email;size:254"`
	OrganizationLabel *string    `gorm:"column:organization_label;size:120"`
	PrincipalID       *guid.ID   `gorm:"column:principal_id"`
	Locale            *string    `gorm:"column:locale;size:8"`
	State             string     `gorm:"column:state;size:32;not null"`
	Note              *string    `gorm:"column:note;size:2000"`
	Version           int        `gorm:"column:version;not null"`
	ArchivedAt        *time.Time `gorm:"column:archived_at"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt         time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	CreatedBy         *guid.ID   `gorm:"column:created_by"`
	UpdatedBy         *guid.ID   `gorm:"column:updated_by"`
}

func (Contact) TableName() string { return "intake_contacts" }
