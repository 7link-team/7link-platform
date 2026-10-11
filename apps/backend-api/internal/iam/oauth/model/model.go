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
	ConsentMode  string    `gorm:"column:consent_mode;size:16;not null"`
	Status       string    `gorm:"column:status;size:16;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

func (Client) TableName() string { return "iam_oauth_clients" }

type AuthorizationCode struct {
	GrantID       string     `gorm:"column:grant_id;size:64;not null"`
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

// DeviceAuthorization is one pending device-flow request. A device with no
// browser and no keyboard shows a short user code and polls; the person approves
// it somewhere they can type. Both identifiers are stored as hashes, like the
// authorization code is, so a stolen database read cannot be redeemed.
//
// PrincipalID is set when somebody approves, which is also what moves the status
// out of pending: a device polls on the status and never sees who approved it
// until a token is issued.
type DeviceAuthorization struct {
	DeviceCodeHash string   `gorm:"column:device_code_hash;size:64;primaryKey"`
	UserCodeHash   string   `gorm:"column:user_code_hash;size:64;not null"`
	ClientID       guid.ID  `gorm:"column:client_id;not null"`
	TenantID       guid.ID  `gorm:"column:tenant_id;not null"`
	Scope          string   `gorm:"column:scope;type:text;not null"`
	Status         string   `gorm:"column:status;size:16;not null"`
	PrincipalID    *guid.ID `gorm:"column:principal_id"`
	// Interval is what the device was told to wait between polls, and
	// LastPolledAt is when it last did. Keeping both is what lets a device that
	// polls too fast be slowed down rather than refused.
	Interval     int        `gorm:"column:interval_seconds;not null"`
	LastPolledAt *time.Time `gorm:"column:last_polled_at"`
	AuthTime     *time.Time `gorm:"column:auth_time"`
	ACR          int        `gorm:"column:acr;not null"`
	AMR          string     `gorm:"column:amr;type:text;not null"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt    time.Time  `gorm:"column:expires_at;not null"`
	ConsumedAt   *time.Time `gorm:"column:consumed_at"`
}

func (DeviceAuthorization) TableName() string { return "iam_oauth_device_authorizations" }

// TokenExchangeGrant is one permitted delegation: a client that may ask for a
// token acting as a subject, bounded to the scopes named here. Nothing is
// implied -- a client with no row cannot exchange at all, and a client with one
// cannot step outside the scopes it lists. Impersonation is never open-ended
// because the pair has to be written down first.
type TokenExchangeGrant struct {
	ID            guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID      guid.ID   `gorm:"column:tenant_id;not null"`
	ClientID      guid.ID   `gorm:"column:client_id;not null"`
	SubjectPrefix string    `gorm:"column:subject_prefix;size:128;not null"`
	Scope         string    `gorm:"column:scope;type:text;not null"`
	Status        string    `gorm:"column:status;size:16;not null"`
	CreatedAt     time.Time `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null"`
}

func (TokenExchangeGrant) TableName() string { return "iam_oauth_exchange_grants" }

type Consent struct {
	GrantID     string    `gorm:"column:grant_id;size:64;not null"`
	PrincipalID guid.ID   `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	ClientID    guid.ID   `gorm:"column:client_id;primaryKey;autoIncrement:false"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	Scope       string    `gorm:"column:scope;type:text;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
	LastUsedAt  time.Time `gorm:"column:last_used_at;not null"`
}

func (Consent) TableName() string { return "iam_oauth_consents" }

// ConsentRequest is one authorization request parked while its subject decides
// on the consent screen. It is addressed by the hash of the opaque challenge
// value carried in the redirect, so the value itself is never stored.
type ConsentRequest struct {
	ChallengeHash string     `gorm:"column:challenge_hash;size:64;primaryKey"`
	ClientID      guid.ID    `gorm:"column:client_id;not null"`
	PrincipalID   guid.ID    `gorm:"column:principal_id;not null"`
	RedirectURI   string     `gorm:"column:redirect_uri;type:text;not null"`
	Scope         string     `gorm:"column:scope;type:text;not null"`
	State         string     `gorm:"column:state;type:text;not null"`
	CodeChallenge string     `gorm:"column:code_challenge;size:128;not null"`
	Nonce         string     `gorm:"column:nonce;size:256;not null"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt     time.Time  `gorm:"column:expires_at;not null"`
	ResolvedAt    *time.Time `gorm:"column:resolved_at"`
}

func (ConsentRequest) TableName() string { return "iam_oauth_consent_requests" }

type RefreshToken struct {
	GrantID     string     `gorm:"column:grant_id;size:64;not null"`
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
