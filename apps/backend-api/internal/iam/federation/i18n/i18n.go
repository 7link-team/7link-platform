// Package federationi18n publishes federation's locale bundle.
package federationi18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const Namespace = "federation"

const (
	InvalidRequest           = Namespace + ".invalid_request"
	IdentityLinkConflict     = Namespace + ".identity_link_conflict"
	LastLoginMethod          = Namespace + ".last_login_method"
	ProviderNotFound         = Namespace + ".provider_not_found"
	ProviderIssuerExists     = Namespace + ".provider_issuer_exists"
	ProviderRoleMissing      = Namespace + ".provider_role_missing"
	ProviderDisabled         = Namespace + ".provider_disabled"
	ProvisioningDisabled     = Namespace + ".provisioning_disabled"
	IdentityUnavailable      = Namespace + ".identity_unavailable"
	EmailUnverified          = Namespace + ".email_unverified"
	OIDCFailed               = Namespace + ".oidc_failed"
	Unavailable              = Namespace + ".unavailable"
	SAMLInvalidRequest       = Namespace + ".saml_invalid_request"
	SAMLInvalidSP            = Namespace + ".saml_invalid_sp"
	SAMLSPNotFound           = Namespace + ".saml_sp_not_found"
	SAMLSPEntityIDExists     = Namespace + ".saml_sp_entity_id_exists"
	SAMLSPDisabled           = Namespace + ".saml_sp_disabled"
	SAMLUnavailable          = Namespace + ".saml_unavailable"
	SAMLKeyManagedExternally = Namespace + ".saml_key_managed_externally"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }
