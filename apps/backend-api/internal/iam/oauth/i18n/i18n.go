// Package oauthi18n publishes the OAuth module's locale bundle.
package oauthi18n

import (
	"embed"
	"strings"

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
	ConsentRequired            = Namespace + ".consent_required"
	ConsentNotFound            = Namespace + ".consent_not_found"

	ConsentTitle  = Namespace + ".consent_title"
	ConsentPrompt = Namespace + ".consent_prompt"
	ConsentAllow  = Namespace + ".consent_allow"
	ConsentDeny   = Namespace + ".consent_deny"

	// scopePrefix namespaces the per-scope explanations shown on the consent
	// screen. Scopes are client configuration, so most deployments will add
	// their own keys under this prefix; a scope with no key falls back to its
	// raw token rather than disappearing from the screen.
	scopePrefix = Namespace + ".scope_"
)

// ScopeKey names the translation for one OAuth scope. Only the characters a
// scope token may legally contain reach the key, so a hostile scope string
// cannot address an unrelated message.
func ScopeKey(scope string) string {
	var builder strings.Builder
	builder.Grow(len(scopePrefix) + len(scope))
	builder.WriteString(scopePrefix)
	for _, r := range scope {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r + ('a' - 'A'))
		default:
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

//go:embed locales/*.json
var locales embed.FS

// Register publishes the OAuth locale bundle under its module namespace.
func Register() error {
	return platformi18n.RegisterFS(Namespace, locales, "locales")
}
