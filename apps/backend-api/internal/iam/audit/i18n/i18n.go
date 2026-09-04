// Package auditi18n publishes the IAM audit module's locale bundle.
package auditi18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	Namespace = "audit"

	InvalidTime         = Namespace + ".invalid_audit_time"
	EventNotFound       = Namespace + ".audit_event_not_found"
	InvalidQuery        = Namespace + ".invalid_audit_query"
	IntegrityFailed     = Namespace + ".audit_integrity_failed"
	Unavailable         = Namespace + ".audit_unavailable"
	AnchorNotEnabled    = Namespace + ".audit_anchor_not_enabled"
	AnchorEmpty         = Namespace + ".audit_anchor_empty"
	AnchorAlreadyExists = Namespace + ".audit_anchor_already_exists"
	AnchorUnavailable   = Namespace + ".audit_anchor_unavailable"
	RetentionInvalid    = Namespace + ".audit_retention_invalid"
	RootSigningDisabled = Namespace + ".audit_root_signing_not_enabled"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }
