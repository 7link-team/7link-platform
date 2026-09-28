package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type MFAEnrollment struct {
	PrincipalID      guid.ID   `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	SecretCiphertext string    `gorm:"column:secret_ciphertext;type:text;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
	ExpiresAt        time.Time `gorm:"column:expires_at;not null"`
}

func (MFAEnrollment) TableName() string { return "iam_mfa_enrollments" }

type RecoveryCode struct {
	PrincipalID guid.ID    `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	CodeHash    string     `gorm:"column:code_hash;type:text;primaryKey"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	UsedAt      *time.Time `gorm:"column:used_at"`
}

func (RecoveryCode) TableName() string { return "iam_recovery_codes" }

type MFAChallenge struct {
	IDHash      string     `gorm:"column:id_hash;type:text;primaryKey"`
	PrincipalID guid.ID    `gorm:"column:principal_id;not null"`
	ReturnURL   string     `gorm:"column:return_url;type:text;not null"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt   time.Time  `gorm:"column:expires_at;not null"`
	ConsumedAt  *time.Time `gorm:"column:consumed_at"`
	Attempts    int        `gorm:"column:attempts;not null"`
}

func (MFAChallenge) TableName() string { return "iam_mfa_challenges" }

type PasskeyUser struct {
	PrincipalID guid.ID   `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	UserHandle  string    `gorm:"column:user_handle;type:text;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
}

func (PasskeyUser) TableName() string { return "iam_passkey_users" }

type Passkey struct {
	IDHash string `gorm:"column:id_hash;type:text;primaryKey"`
	// PublicID is the snowflake the API addresses the passkey by; the
	// credential hash stays internal. NULL only until the start-up backfill
	// reaches a row created before the column existed.
	PublicID             *guid.ID   `gorm:"column:public_id"`
	PrincipalID          guid.ID    `gorm:"column:principal_id;not null"`
	Name                 string     `gorm:"column:name;type:text;not null"`
	CredentialCiphertext string     `gorm:"column:credential_ciphertext;type:text;not null"`
	SignCount            uint32     `gorm:"column:sign_count;not null"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;not null"`
	LastUsedAt           *time.Time `gorm:"column:last_used_at"`
}

func (Passkey) TableName() string { return "iam_passkeys" }

type PasskeyChallenge struct {
	IDHash      string     `gorm:"column:id_hash;type:text;primaryKey"`
	Kind        string     `gorm:"column:kind;type:text;not null"`
	PrincipalID *guid.ID   `gorm:"column:principal_id"`
	ReturnURL   string     `gorm:"column:return_url;type:text;not null"`
	SessionData string     `gorm:"column:session_data;type:text;not null"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt   time.Time  `gorm:"column:expires_at;not null"`
	ConsumedAt  *time.Time `gorm:"column:consumed_at"`
}

func (PasskeyChallenge) TableName() string { return "iam_passkey_challenges" }

type StepUpChallenge struct {
	IDHash      string     `gorm:"column:id_hash;type:text;primaryKey"`
	PrincipalID guid.ID    `gorm:"column:principal_id;not null"`
	SessionHash string     `gorm:"column:session_hash;type:text;not null"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt   time.Time  `gorm:"column:expires_at;not null"`
	ConsumedAt  *time.Time `gorm:"column:consumed_at"`
	Attempts    int        `gorm:"column:attempts;not null"`
}

func (StepUpChallenge) TableName() string { return "iam_stepup_challenges" }
