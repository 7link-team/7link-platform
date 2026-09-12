package oauth

import (
	"fmt"

	gormx "7link.com/platform/pkg/extension/gorm"
)

// foreignTables carries the names of the tables oauth reads but does not own.
// Every owner is activated before oauth and publishes its set as a capability,
// which oauth resolves during injection; a set that never arrived stays empty
// and every lookup fails, so a missing publication is an error rather than a
// name oauth would otherwise have to invent from its own prefix.
type foreignTables struct {
	access     gormx.TableSet
	principal  gormx.TableSet
	credential gormx.TableSet
	directory  gormx.TableSet
}

// bind records what each owner published. It is what the module hands the
// repository once injection has resolved the four capabilities.
func (t *foreignTables) bind(access, principal, credential, directory gormx.TableSet) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published tables", errRepositoryUnavailable)
	}
	t.access, t.principal = access, principal
	t.credential, t.directory = credential, directory
	return nil
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

// credentialTable returns the physical name authn published for a logical
// table.
func (t *foreignTables) credentialTable(logical string) (string, error) {
	if t == nil {
		return "", fmt.Errorf("%w: no published credential tables", errRepositoryUnavailable)
	}
	return publishedTableName(t.credential, "authn", logical)
}

// directoryTable returns the physical name organization published for a
// logical table.
func (t *foreignTables) directoryTable(logical string) (string, error) {
	if t == nil {
		return "", fmt.Errorf("%w: no published directory tables", errRepositoryUnavailable)
	}
	return publishedTableName(t.directory, "organization", logical)
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
