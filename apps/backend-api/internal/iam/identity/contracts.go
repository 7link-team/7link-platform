// Package identity owns IAM principals, service accounts, and identity contracts.
package identity

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	gormx "7link.com/platform/pkg/extension/gorm"
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

// ServiceAccountValidator checks a real automation principal's tenant ownership,
// membership, status and expiry using the caller's database transaction.
type ServiceAccountValidator func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error)

// IdentityProvisioner is the identity capability shared with SCIM and federation.
type IdentityProvisioner interface {
	CreateProvisionedTo(context.Context, *gorm.DB, guid.ID, ProvisionedPrincipalInput) (Principal, error)
	ReplaceProvisionedTo(context.Context, *gorm.DB, guid.ID, guid.ID, ProvisionedPrincipalInput) (Principal, error)
	Get(context.Context, guid.ID, guid.ID) (Principal, error)
	EnsureExternalPrincipal(context.Context, *gorm.DB, guid.ID, string, string, time.Time) (guid.ID, bool, error)
}

// PrincipalStateReceiver is how identity hands its principal-state capability to
// a module activated before it. PrincipalStateStore is an ordinary provider for
// the modules activated after identity -- oauth and federation resolve it
// directly -- but authn cannot, for the reason PrincipalTableSetReceiver
// explains, so identity pushes the same capability instead.
type PrincipalStateReceiver struct {
	Bind func(PrincipalStateStore) error
}

// PrincipalTableSetReceiver is how identity hands the names of its own tables to
// a module that JOINs them in one statement. It is named for its direction,
// because it is the mirror image of core.PrincipalTableSetConnector: there core
// declares the connector and identity fills it, and here the consumer provides
// a receiver and identity pushes into it.
//
// The mirror exists because authn is activated *earlier* than identity, which
// rules out both ordinary shapes: authn resolving an identity type would have to
// declare identity and fail the registration-order check, and identity resolving
// an authn type would have to import authn, which already imports identity. With
// the type declared here, identity resolves a type of its own -- which the
// dependency rule does not record, because the type belongs to the module
// resolving it -- and no import direction changes.
type PrincipalTableSetReceiver struct {
	Bind func(gormx.TableSet) error
}

// DirectoryTableSetConnector lets the module that owns the directory tables
// publish their names to identity. identity reads the tenant table without
// owning it and cannot name it itself: the owner is activated after identity,
// so the name arrives as a set rather than as a prefix identity would apply on
// its own.
type DirectoryTableSetConnector struct {
	Bind func(gormx.TableSet) error
}

// OAuthTableSetConnector does the same for the OAuth tables identity reads when
// a principal's security state is revoked.
type OAuthTableSetConnector struct {
	Bind func(gormx.TableSet) error
}
