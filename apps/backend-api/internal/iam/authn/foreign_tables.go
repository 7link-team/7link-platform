package authn

import (
	"fmt"

	gormx "7link.com/platform/pkg/extension/gorm"
)

// foreignTables carries the table names of the tables authn reads but does not
// own. Three routes fill it, because which one applies depends on when the owner
// is activated relative to authn:
//
//   - an owner activated after authn (organization, oauth) binds its set
//     through a connector authn declares;
//   - an owner activated before authn (core) is an ordinary provider authn
//     resolves;
//   - identity is neither, because authn already imports it: authn cannot
//     resolve an identity type without declaring a dependency the registration
//     order rejects, and identity cannot resolve an authn type without an import
//     cycle. So identity pushes, through the receiver it declares and authn
//     provides -- see identity.PrincipalTableSetReceiver.
//
// Whichever route, a set that never arrived stays empty and every lookup fails,
// so a missing publication is an error rather than an empty table name in a
// statement.
type foreignTables struct {
	directory gormx.TableSet
	oauth     gormx.TableSet
	access    gormx.TableSet
	principal gormx.TableSet
}

// bindDirectory records the table names the organization module published to
// authn. It is what the connector authn provides hands to its owner.
func (t *foreignTables) bindDirectory(published gormx.TableSet) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published directory tables", errRepositoryUnavailable)
	}
	t.directory = published
	return nil
}

// bindAccess records the table names the core module published to authn. core
// is activated before authn, so it is an ordinary provider rather than a
// connector; the holder is shared so that the binding still reaches every
// repository clone.
func (t *foreignTables) bindAccess(published gormx.TableSet) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published access tables", errRepositoryUnavailable)
	}
	t.access = published
	return nil
}

// bindPrincipal records the table names the identity module published to authn.
// identity pushes them itself: see identity.PrincipalTableSetConnector for why
// neither module can resolve the other's type here.
func (t *foreignTables) bindPrincipal(published gormx.TableSet) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published principal tables", errRepositoryUnavailable)
	}
	t.principal = published
	return nil
}

// bindOAuth records the table names the oauth module published to authn.
func (t *foreignTables) bindOAuth(published gormx.TableSet) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published oauth tables", errRepositoryUnavailable)
	}
	t.oauth = published
	return nil
}

// directoryTable returns the physical name organization published for a
// logical table.
func (t *foreignTables) directoryTable(logical string) (string, error) {
	if t == nil {
		return "", fmt.Errorf("%w: no published directory tables", errRepositoryUnavailable)
	}
	return publishedTableName(t.directory, "organization", logical)
}

// accessTable returns the physical name core published for a logical table.
func (t *foreignTables) accessTable(logical string) (string, error) {
	if t == nil {
		return "", fmt.Errorf("%w: no published access tables", errRepositoryUnavailable)
	}
	return publishedTableName(t.access, "core", logical)
}

// principalTable returns the physical name identity published for a logical
// table.
func (t *foreignTables) principalTable(logical string) (string, error) {
	if t == nil {
		return "", fmt.Errorf("%w: no published principal tables", errRepositoryUnavailable)
	}
	return publishedTableName(t.principal, "identity", logical)
}

// oauthTable returns the physical name oauth published for a logical table.
func (t *foreignTables) oauthTable(logical string) (string, error) {
	if t == nil {
		return "", fmt.Errorf("%w: no published oauth tables", errRepositoryUnavailable)
	}
	return publishedTableName(t.oauth, "oauth", logical)
}

// bind records every owner's set at once. It is what an exported binder hands
// a repository built outside the module, where no connector has run.
func (t *foreignTables) bind(directory, oauth, access, principal gormx.TableSet) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published tables", errRepositoryUnavailable)
	}
	t.directory, t.oauth = directory, oauth
	t.access, t.principal = access, principal
	return nil
}

// publishedTableName resolves one logical table in what an owner published. An
// empty set means the owner has published nothing, and a missing name means it
// published something incomplete; both are errors rather than a table name
// this module would otherwise have to invent from its own prefix.
func publishedTableName(published gormx.TableSet, owner, logical string) (string, error) {
	if published.Len() == 0 {
		return "", fmt.Errorf("%s has not published its table names: %w", owner, errRepositoryUnavailable)
	}
	name, ok := published.Lookup(logical)
	if !ok {
		return "", fmt.Errorf("%s did not publish %s: %w", owner, logical, errRepositoryUnavailable)
	}
	return name.String(), nil
}
