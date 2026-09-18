// Package model contains the identity module's persisted row types.
package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type Principal struct {
	ID                 guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	LoginName          string     `gorm:"column:login_name;type:text;not null"`
	Email              string     `gorm:"column:email;type:text;not null"`
	DisplayName        string     `gorm:"column:display_name;type:text;not null"`
	Avatar             string     `gorm:"column:avatar;type:text;not null;default:''"`
	Status             string     `gorm:"column:status;type:text;not null"`
	EmailVerified      bool       `gorm:"column:email_verified;not null"`
	ActivationRequired bool       `gorm:"column:activation_required;not null"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null"`
	DisabledAt         *time.Time `gorm:"column:disabled_at"`
}

func (Principal) TableName() string { return "iam_principals" }

// PrincipalLifecycle preserves retention decisions and irreversible erasure state.
type PrincipalLifecycle struct {
	PrincipalID  guid.ID    `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	Version      int64      `gorm:"column:version;not null"`
	RetainUntil  time.Time  `gorm:"column:retain_until;not null"`
	LegalHold    bool       `gorm:"column:legal_hold;not null"`
	Reference    string     `gorm:"column:reference;type:text;not null"`
	AnonymizedAt *time.Time `gorm:"column:anonymized_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null"`
}

func (PrincipalLifecycle) TableName() string { return "iam_principal_lifecycles" }

type ServiceAccount struct {
	PrincipalID   guid.ID    `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	OwnerTenantID guid.ID    `gorm:"column:owner_tenant_id;not null"`
	Description   string     `gorm:"column:description;type:text;not null"`
	Status        string     `gorm:"column:status;type:text;not null"`
	ExpiresAt     *time.Time `gorm:"column:expires_at"`
	TokenVersion  int64      `gorm:"column:token_version;not null"`
	Version       int64      `gorm:"column:version;not null"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null"`
	DeletedAt     *time.Time `gorm:"column:deleted_at"`
}

func (ServiceAccount) TableName() string { return "iam_service_accounts" }

type ServiceAccountCredential struct {
	ID          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	PrincipalID guid.ID    `gorm:"column:principal_id;not null"`
	Name        string     `gorm:"column:name;type:text;not null"`
	SecretHash  string     `gorm:"column:secret_hash;type:text;not null"`
	Scopes      string     `gorm:"column:scopes;type:text;not null"`
	ExpiresAt   *time.Time `gorm:"column:expires_at"`
	LastUsedAt  *time.Time `gorm:"column:last_used_at"`
	RevokedAt   *time.Time `gorm:"column:revoked_at"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
}

func (ServiceAccountCredential) TableName() string { return "iam_service_account_credentials" }
