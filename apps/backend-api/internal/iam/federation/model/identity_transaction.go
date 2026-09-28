package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// IdentityLinkTransaction separates account linking and reauthentication from
// ordinary login/JIT and keeps callback completion one-time across replicas.
type IdentityLinkTransaction struct {
	IDHash                 string     `gorm:"column:id_hash;primaryKey;size:64"`
	PrincipalID            guid.ID    `gorm:"column:principal_id;not null"`
	ProviderID             guid.ID    `gorm:"column:provider_id;not null"`
	TenantID               guid.ID    `gorm:"column:tenant_id;not null"`
	SessionHash            string     `gorm:"column:session_hash;size:64;not null"`
	CredentialVersion      int64      `gorm:"column:credential_version;not null"`
	Purpose                string     `gorm:"column:purpose;size:32;not null"`
	ProviderFingerprint    string     `gorm:"column:provider_fingerprint;size:64;not null"`
	ChallengeHash          string     `gorm:"column:challenge_hash;size:64;not null"`
	ExpectedSubject        string     `gorm:"column:expected_subject;size:128;not null"`
	CodeVerifierCiphertext string     `gorm:"column:code_verifier_ciphertext;type:text;not null"`
	CallbackURL            string     `gorm:"column:callback_url;type:text;not null"`
	ReturnURL              string     `gorm:"column:return_url;type:text;not null"`
	ExternalSubject        string     `gorm:"column:external_subject;size:128;not null"`
	Email                  string     `gorm:"column:email;size:255;not null"`
	DisplayName            string     `gorm:"column:display_name;size:128;not null"`
	CreatedAt              time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt              time.Time  `gorm:"column:expires_at;not null"`
	VerifiedAt             *time.Time `gorm:"column:verified_at"`
	ConsumedAt             *time.Time `gorm:"column:consumed_at"`
}

func (IdentityLinkTransaction) TableName() string { return "iam_identity_link_transactions" }
