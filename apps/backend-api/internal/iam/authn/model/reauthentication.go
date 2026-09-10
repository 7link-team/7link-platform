package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// ReauthenticationChallenge authorizes exactly one sensitive operation after a
// new credential check in the same browser session.
type ReauthenticationChallenge struct {
	IDHash            string     `gorm:"column:id_hash;primaryKey;size:64"`
	PrincipalID       guid.ID    `gorm:"column:principal_id;not null"`
	SessionHash       string     `gorm:"column:session_hash;size:64;not null"`
	CredentialVersion int64      `gorm:"column:credential_version;not null"`
	Purpose           string     `gorm:"column:purpose;size:64;not null"`
	Target            string     `gorm:"column:target;size:512;not null"`
	Method            string     `gorm:"column:method;size:32;not null"`
	SessionData       string     `gorm:"column:session_data;type:text;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt         time.Time  `gorm:"column:expires_at;not null"`
	VerifiedAt        *time.Time `gorm:"column:verified_at"`
	ConsumedAt        *time.Time `gorm:"column:consumed_at"`
	Attempts          int        `gorm:"column:attempts;not null"`
}

func (ReauthenticationChallenge) TableName() string { return "iam_reauth_challenges" }
