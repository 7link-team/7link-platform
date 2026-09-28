package authn

import (
	"fmt"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// foreignTables carries the names of the tables authn reads but does not own,
// in the form each owner hands them over. Three routes fill it, because which
// one applies depends on when the owner is activated relative to authn:
//
//   - an owner activated after authn (organization, oauth) imports authn, so
//     authn declares the shape it needs and the owner fills it through a
//     connector authn provides;
//   - an owner activated before authn (core) is imported by authn, so it
//     declares the struct and authn resolves it as an ordinary provider;
//   - identity is neither, because authn already imports it: authn cannot
//     resolve an identity type without declaring a dependency the registration
//     order rejects, and identity cannot resolve an authn type without an import
//     cycle. So identity pushes its own published struct, through the receiver
//     it declares and authn provides -- see identity.PrincipalTableNamesReceiver.
//
// Whether the names arrived is checked once, when the module initializes -- the
// registry has run every Provide and Bind by then -- so an owner that published
// nothing fails startup rather than the first query that needs it.
type foreignTables struct {
	directory DirectoryTableNames
	oauth     OAuthTableNames
	access    core.AccessTableNames
	principal identity.PrincipalTableNames
}

// bindDirectory records the names the organization module filled in. It is what
// the connector authn provides hands to its owner.
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

// bindAccess records the names the core module published to authn. core is
// activated before authn, so it is an ordinary provider rather than a
// connector; the holder is shared so that the binding still reaches every
// repository clone.
func (t *foreignTables) bindAccess(published core.AccessTableNames) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published access tables", errRepositoryUnavailable)
	}
	t.access = published
	return nil
}

// bindPrincipal records the names the identity module pushed to authn. identity
// pushes them itself: see identity.PrincipalTableNamesReceiver for why neither
// module can resolve the other's type here.
func (t *foreignTables) bindPrincipal(published identity.PrincipalTableNames) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published principal tables", errRepositoryUnavailable)
	}
	t.principal = published
	return nil
}

// bindOAuth records the names the oauth module filled in.
func (t *foreignTables) bindOAuth(published OAuthTableNames) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published oauth tables", errRepositoryUnavailable)
	}
	for _, entry := range []struct {
		name  gormx.TableName
		field string
	}{{published.Clients, "Clients"}, {published.RefreshTokens, "RefreshTokens"}} {
		if entry.name.IsZero() {
			return fmt.Errorf("%w: oauth left %s empty", errRepositoryUnavailable, entry.field)
		}
	}
	t.oauth = published
	return nil
}

// bind records every owner's names at once. It is what an exported binder hands
// a repository built outside the module, where no connector has run.
func (t *foreignTables) bind(
	directory DirectoryTableNames, oauth OAuthTableNames,
	access core.AccessTableNames, principal identity.PrincipalTableNames,
) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published tables", errRepositoryUnavailable)
	}
	if err := t.bindDirectory(directory); err != nil {
		return err
	}
	if err := t.bindOAuth(oauth); err != nil {
		return err
	}
	if err := t.bindAccess(access); err != nil {
		return err
	}
	return t.bindPrincipal(principal)
}

// bound reports whether every owner published. Until they have, no statement
// that reads a table authn does not own may run.
func (t *foreignTables) bound() bool {
	return t != nil && t.access.Bound() && t.principal.Bound() &&
		t.directory.Bound() && t.oauth.Bound()
}
