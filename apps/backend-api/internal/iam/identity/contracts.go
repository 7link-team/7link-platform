// Package identity owns IAM principals, service accounts, and identity contracts.
package identity

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// Principal is the stable identity representation shared with provisioning.
type Principal struct {
	ID                 guid.ID   `json:"id"`
	LoginName          string    `json:"login_name"`
	Email              string    `json:"email,omitempty"`
	EmailVerified      bool      `json:"email_verified"`
	ActivationRequired bool      `json:"activation_required"`
	DisplayName        string    `json:"display_name"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ProvisionedPrincipalInput is the canonical transactional SCIM identity command.
type ProvisionedPrincipalInput struct {
	LoginName    string
	DisplayName  string
	Email        string
	PasswordHash string
	Active       bool
}

// InvitationPrincipalCreator creates a principal while accepting an invitation
// through the organization's caller-owned transaction.
type InvitationPrincipalCreator func(context.Context, *gorm.DB, guid.ID, string, string, string, string) (guid.ID, error)

// IdentityProvisioner is the identity capability shared with SCIM and federation.
type IdentityProvisioner interface {
	CreateProvisionedTo(context.Context, *gorm.DB, guid.ID, ProvisionedPrincipalInput) (Principal, error)
	ReplaceProvisionedTo(context.Context, *gorm.DB, guid.ID, guid.ID, ProvisionedPrincipalInput) (Principal, error)
	Get(context.Context, guid.ID, guid.ID) (Principal, error)
	EnsureExternalPrincipal(context.Context, *gorm.DB, guid.ID, string, string, time.Time) (guid.ID, bool, error)
}
