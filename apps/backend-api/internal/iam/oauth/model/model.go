// Package model contains the OAuth module's persisted row types.
package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type Client struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null"`
	SecretHash   string    `gorm:"column:secret_hash;type:text;not null"`
	Name         string    `gorm:"column:name;size:128;not null"`
	RedirectURIs string    `gorm:"column:redirect_uris;type:text;not null"`
	GrantTypes   string    `gorm:"column:grant_types;type:text;not null"`
	Scopes       string    `gorm:"column:scopes;type:text;not null"`
	PublicClient bool      `gorm:"column:public_client;not null"`
	Status       string    `gorm:"column:status;size:16;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

func (Client) TableName() string { return "iam_oauth_clients" }

type AuthorizationCode struct {
	CodeHash      string     `gorm:"column:code_hash;size:64;primaryKey"`
	ClientID      guid.ID    `gorm:"column:client_id;not null"`
	PrincipalID   guid.ID    `gorm:"column:principal_id;not null"`
	RedirectURI   string     `gorm:"column:redirect_uri;type:text;not null"`
	Scope         string     `gorm:"column:scope;type:text;not null"`
	CodeChallenge string     `gorm:"column:code_challenge;size:128;not null"`
	Nonce         string     `gorm:"column:nonce;size:256;not null"`
	AuthTime      time.Time  `gorm:"column:auth_time;not null"`
	ACR           int        `gorm:"column:acr;not null"`
	AMR           string     `gorm:"column:amr;type:text;not null"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt     time.Time  `gorm:"column:expires_at;not null"`
	ConsumedAt    *time.Time `gorm:"column:consumed_at"`
}

func (AuthorizationCode) TableName() string { return "iam_oauth_codes" }

type Consent struct {
	PrincipalID guid.ID   `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	ClientID    guid.ID   `gorm:"column:client_id;primaryKey;autoIncrement:false"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	Scope       string    `gorm:"column:scope;type:text;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
	LastUsedAt  time.Time `gorm:"column:last_used_at;not null"`
}

func (Consent) TableName() string { return "iam_oauth_consents" }

type RefreshToken struct {
	IDHash      string     `gorm:"column:id_hash;size:64;primaryKey"`
	FamilyID    string     `gorm:"column:family_id;size:64;not null"`
	PrincipalID guid.ID    `gorm:"column:principal_id;not null"`
	ClientID    guid.ID    `gorm:"column:client_id;not null"`
	Scope       string     `gorm:"column:scope;type:text;not null"`
	AuthTime    time.Time  `gorm:"column:auth_time;not null"`
	ACR         int        `gorm:"column:acr;not null"`
	AMR         string     `gorm:"column:amr;type:text;not null"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt   time.Time  `gorm:"column:expires_at;not null"`
	UsedAt      *time.Time `gorm:"column:used_at"`
	RevokedAt   *time.Time `gorm:"column:revoked_at"`
}

func (RefreshToken) TableName() string { return "iam_refresh_tokens" }
