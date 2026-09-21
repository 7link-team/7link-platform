package authn

const (
	RateLimitScopeMetadata  = "authn.rate-limit-scope"
	RateLimitAuthentication = "authentication"
	RateLimitOAuth          = "oauth"
)

// RateLimitMetadata explicitly declares which shared authentication budget an
// operation consumes. Transport middleware reads this declaration without
// inferring security policy from a URI, tag, or operation identifier.
func RateLimitMetadata(scope string) map[string]any {
	return map[string]any{RateLimitScopeMetadata: scope}
}
