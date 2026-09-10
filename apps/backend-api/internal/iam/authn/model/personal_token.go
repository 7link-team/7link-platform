package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// PersonalToken stores a one-way digest; the bearer secret is returned only once.
type PersonalToken struct {
	ID                guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TokenHash         string     `gorm:"column:token_hash;size:64;not null;uniqueIndex"`
	PrincipalID       guid.ID    `gorm:"column:principal_id;not null"`
	TenantID          guid.ID    `gorm:"column:tenant_id;not null"`
	Name              string     `gorm:"column:name;size:128;not null"`
	Permissions       string     `gorm:"column:permissions;type:text;not null"`
	CredentialVersion int64      `gorm:"column:credential_version;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt         time.Time  `gorm:"column:expires_at;not null"`
	RevokedAt         *time.Time `gorm:"column:revoked_at"`
}

func (PersonalToken) TableName() string { return "iam_personal_tokens" }
