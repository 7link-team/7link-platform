package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type Provider struct {
	ID                     guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID               guid.ID   `gorm:"column:tenant_id;not null"`
	Name                   string    `gorm:"column:name;size:128;not null"`
	ProviderType           string    `gorm:"column:provider_type;size:16;not null"`
	Issuer                 string    `gorm:"column:issuer;size:255;not null"`
	ClientID               string    `gorm:"column:client_id;size:128;not null"`
	ClientSecretCiphertext string    `gorm:"column:client_secret_ciphertext;type:text;not null"`
	Scopes                 string    `gorm:"column:scopes;size:255;not null"`
	AutoProvision          bool      `gorm:"column:auto_provision;not null"`
	DefaultRoleID          *guid.ID  `gorm:"column:default_role_id"`
	Status                 string    `gorm:"column:status;size:16;not null"`
	CreatedAt              time.Time `gorm:"column:created_at;not null"`
	UpdatedAt              time.Time `gorm:"column:updated_at;not null"`
}

func (Provider) TableName() string { return "iam_identity_providers" }

type IdentityLink struct {
	ProviderID      guid.ID   `gorm:"column:provider_id;primaryKey;autoIncrement:false"`
	TenantID        guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ExternalSubject string    `gorm:"column:external_subject;primaryKey;size:128;autoIncrement:false"`
	PrincipalID     guid.ID   `gorm:"column:principal_id;not null"`
	Email           string    `gorm:"column:email;size:255;not null"`
	DisplayName     string    `gorm:"column:display_name;size:128;not null"`
	LastLoginAt     time.Time `gorm:"column:last_login_at;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null"`
	UpdatedAt       time.Time `gorm:"column:updated_at;not null"`
}

func (IdentityLink) TableName() string { return "iam_identity_links" }

type SAMLServiceProvider struct {
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	Name        string    `gorm:"column:name;size:128;not null"`
	EntityID    string    `gorm:"column:entity_id;size:255;not null"`
	MetadataXML string    `gorm:"column:metadata_xml;type:text;not null"`
	Status      string    `gorm:"column:status;size:16;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null"`
}

func (SAMLServiceProvider) TableName() string { return "iam_saml_service_providers" }

type SAMLIDPKey struct {
	KeyID         string     `gorm:"column:key_id;primaryKey;size:128"`
	CertPEM       string     `gorm:"column:cert_pem;type:text;not null"`
	KeyCiphertext string     `gorm:"column:key_ciphertext;type:text;not null"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null"`
	RotatedAt     *time.Time `gorm:"column:rotated_at"`
}

func (SAMLIDPKey) TableName() string { return "iam_saml_idp_keys" }

// ReplayClaim persists a protocol identifier's consumption across replicas.
type ReplayClaim struct {
	ClaimKey  string    `gorm:"column:claim_key;primaryKey;size:255"`
	ExpiresAt time.Time `gorm:"column:expires_at;not null;index"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (ReplayClaim) TableName() string { return "iam_federation_replay_claims" }
