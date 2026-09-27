package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// AccountViewToken is independent of target login sessions and personal tokens.
type AccountViewToken struct {
	ID                      guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TokenHash               string     `gorm:"column:token_hash;size:64;not null"`
	ActorID                 guid.ID    `gorm:"column:actor_id;not null"`
	PrincipalID             guid.ID    `gorm:"column:principal_id;not null"`
	ActorSessionHash        string     `gorm:"column:actor_session_hash;size:64;not null"`
	ActorCredentialVersion  int64      `gorm:"column:actor_credential_version;not null"`
	TargetCredentialVersion int64      `gorm:"column:target_credential_version;not null"`
	ScopeType               string     `gorm:"column:scope_type;size:16;not null"`
	ScopeID                 guid.ID    `gorm:"column:scope_id;not null"`
	Mode                    string     `gorm:"column:mode;size:16;not null;default:read_only"`
	CreatedAt               time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt               time.Time  `gorm:"column:expires_at;not null"`
	LastUsedAt              *time.Time `gorm:"column:last_used_at"`
	RevokedAt               *time.Time `gorm:"column:revoked_at"`
}

func (AccountViewToken) TableName() string { return "iam_account_view_tokens" }
