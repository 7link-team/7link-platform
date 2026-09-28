// Package identityi18n publishes the identity module's locale bundle.
package identityi18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	Namespace                   = "identity"
	ClosureConflict             = Namespace + ".closure_conflict"
	PlatformAccessBlocksClosure = Namespace + ".platform_access_blocks_closure"

	PrincipalNotFound                = Namespace + ".principal_not_found"
	LifecycleVersionConflict         = Namespace + ".principal_lifecycle_version_conflict"
	PrincipalRetained                = Namespace + ".principal_retained"
	PrincipalAnonymized              = Namespace + ".principal_anonymized"
	PrincipalSharedBeyondTenant      = Namespace + ".principal_shared_beyond_tenant"
	SuspensionRequired               = Namespace + ".principal_suspension_required"
	AnonymizationConfirmation        = Namespace + ".principal_anonymization_confirmation"
	LoginNameExists                  = Namespace + ".login_name_exists"
	InvalidPrincipal                 = Namespace + ".invalid_principal"
	IdentityUnavailable              = Namespace + ".identity_unavailable"
	ServiceAccountNotFound           = Namespace + ".service_account_not_found"
	ServiceAccountCredentialNotFound = Namespace + ".service_account_credential_not_found"
	ServiceAccountVersionConflict    = Namespace + ".service_account_version_conflict"
	ServiceAccountCredentialLimit    = Namespace + ".service_account_credential_limit"
	InvalidServiceAccount            = Namespace + ".invalid_service_account"
)

//go:embed locales/*.json
var locales embed.FS

// Register publishes the identity locale bundle under its module namespace.
func Register() error {
	return platformi18n.RegisterFS(Namespace, locales, "locales")
}
