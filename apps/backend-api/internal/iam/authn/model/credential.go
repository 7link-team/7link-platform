// Package model contains the authentication module's persisted row types.
package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type Credential struct {
	PrincipalID           guid.ID    `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	PasswordHash          string     `gorm:"column:password_hash;type:text;not null"`
	TOTPSecret            string     `gorm:"column:totp_secret;type:text;not null"`
	MFARequired           bool       `gorm:"column:mfa_required;not null"`
	FailedAttempts        int        `gorm:"column:failed_attempts;not null"`
	LockedUntil           *time.Time `gorm:"column:locked_until"`
	PasswordChangedAt     time.Time  `gorm:"column:password_changed_at;not null"`
	TOTPLastUsedStep      int64      `gorm:"column:totp_last_used_step;not null"`
	CredentialVersion     int64      `gorm:"column:credential_version;not null"`
	RecoveryCooldownUntil *time.Time `gorm:"column:recovery_cooldown_until"`
	UpdatedAt             time.Time  `gorm:"column:updated_at;not null"`
}

func (Credential) TableName() string { return "iam_credentials" }

type Session struct {
	IDHash string `gorm:"column:id_hash;type:text;primaryKey"`
	// PublicID is the snowflake the API addresses the session by; the hash
	// stays internal. NULL only until the start-up backfill reaches a row
	// created before the column existed.
	PublicID          *guid.ID   `gorm:"column:public_id"`
	PrincipalID       guid.ID    `gorm:"column:principal_id;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null"`
	AuthTime          time.Time  `gorm:"column:auth_time;not null"`
	ACR               int        `gorm:"column:acr;not null"`
	AMR               string     `gorm:"column:amr;type:text;not null"`
	LastSeenAt        time.Time  `gorm:"column:last_seen_at;not null"`
	ExpiresAt         time.Time  `gorm:"column:expires_at;not null"`
	AbsoluteExpiresAt time.Time  `gorm:"column:absolute_expires_at;not null"`
	RevokedAt         *time.Time `gorm:"column:revoked_at"`
	IPAddress         string     `gorm:"column:ip_address;type:text;not null"`
	UserAgent         string     `gorm:"column:user_agent;type:text;not null"`
}

func (Session) TableName() string { return "iam_sessions" }

type PasswordHistory struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	PrincipalID  guid.ID   `gorm:"column:principal_id;not null"`
	PasswordHash string    `gorm:"column:password_hash;type:text;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
}

func (PasswordHistory) TableName() string { return "iam_password_history" }
