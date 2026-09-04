// Package oauthi18n publishes the OAuth module's locale bundle.
package oauthi18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	Namespace = "oauth"

	InvalidClientConfiguration = Namespace + ".invalid_client_configuration"
	ClientNotFound             = Namespace + ".client_not_found"
	InvalidRequest             = Namespace + ".invalid_request"
	InvalidGrant               = Namespace + ".invalid_grant"
	InvalidClient              = Namespace + ".invalid_client"
	InvalidToken               = Namespace + ".invalid_token"
	InsufficientScope          = Namespace + ".insufficient_scope"
	UnsupportedTokenType       = Namespace + ".unsupported_token_type"
	ServerError                = Namespace + ".server_error"
)

//go:embed locales/*.json
var locales embed.FS

// Register publishes the OAuth locale bundle under its module namespace.
func Register() error {
	return platformi18n.RegisterFS(Namespace, locales, "locales")
}
