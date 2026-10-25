package authn

// ReauthenticationScopeChange binds a fresh browser-session proof to the exact
// requirement target supplied by delivery. The existing consumer enforces its
// five-minute lifetime, single use and same-transaction rollback semantics.
const ReauthenticationScopeChange = "scope_change"
