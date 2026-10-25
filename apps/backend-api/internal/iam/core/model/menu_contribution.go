package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// MenuContribution retains deleted menu identities so synchronization cannot
// mistake an administrator's deletion for an installation that never ran.
type MenuContribution struct {
	TenantID   guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID         guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	Module     string    `gorm:"column:module;size:64;not null"`
	PageKey    string    `gorm:"column:page_key;size:128;not null"`
	MenuID     *guid.ID  `gorm:"column:menu_id"`
	Version    int64     `gorm:"column:version;not null"`
	SeededHash string    `gorm:"column:seeded_hash;size:64;not null"`
	Customized bool      `gorm:"column:customized;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;not null"`
	UpdatedAt  time.Time `gorm:"column:updated_at;not null"`
}

func (MenuContribution) TableName() string { return "iam_menu_contributions" }
