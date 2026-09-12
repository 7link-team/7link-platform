package identity

import (
	"fmt"

	"7link.com/platform/internal/iam/core"
)

// foreignTables carries the names of the tables identity reads but does not
// own, in the form each owner hands them over.
//
// Which form that is depends on which way the import runs. core is activated
// before identity and identity imports it, so core declares the struct and
// identity resolves it. organization and oauth are activated after identity and
// import it, so identity declares the shape it needs and they fill it through
// the connectors identity provides.
//
// Whether the names arrived is checked once, when the module initializes -- the
// registry has run every Provide and Bind by then -- so an owner that published
// nothing fails startup rather than the first query that needs it.
type foreignTables struct {
	access    core.AccessTableNames
	directory DirectoryTableNames
	oauth     OAuthTableNames
}

// bindAccess records the names the core module published. core is activated
// before identity, so it is an ordinary provider rather than a connector; the
// holder is shared so the binding reaches every clone.
func (t *foreignTables) bindAccess(published core.AccessTableNames) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published access tables", errRepositoryUnavailable)
	}
	t.access = published
	return nil
}

// bindDirectory records the names the organization module filled in. It is what
// the connector identity provides hands to its owner.
func (t *foreignTables) bindDirectory(published DirectoryTableNames) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published directory tables", errRepositoryUnavailable)
	}
	if published.Tenants.IsZero() {
		return fmt.Errorf("%w: organization left Tenants empty", errRepositoryUnavailable)
	}
	t.directory = published
	return nil
}

// bindOAuth records the names the oauth module filled in.
func (t *foreignTables) bindOAuth(published OAuthTableNames) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published oauth tables", errRepositoryUnavailable)
	}
	if published.RefreshTokens.IsZero() {
		return fmt.Errorf("%w: oauth left RefreshTokens empty", errRepositoryUnavailable)
	}
	t.oauth = published
	return nil
}

// bound reports whether every owner published. Until they have, no statement
// that reads a table identity does not own may run.
func (t *foreignTables) bound() bool {
	return t != nil && t.access.Bound() && !t.directory.Tenants.IsZero() && !t.oauth.RefreshTokens.IsZero()
}
