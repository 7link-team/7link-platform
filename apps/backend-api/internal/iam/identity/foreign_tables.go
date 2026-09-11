package identity

import (
	"fmt"

	gormx "7link.com/platform/pkg/extension/gorm"
)

// foreignTables carries the table names of the tables identity reads but does
// not own. The modules that own them bind their sets through the connectors
// identity declares; a set that never arrived stays empty and every lookup
// fails, so a missing publication is an error rather than an empty table name
// in a statement.
type foreignTables struct {
	directory gormx.TableSet
	oauth     gormx.TableSet
}

// bindDirectory records the table names the organization module published to
// identity. It is what the connector identity provides hands to its owner.
func (t *foreignTables) bindDirectory(published gormx.TableSet) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published directory tables", errRepositoryUnavailable)
	}
	t.directory = published
	return nil
}

// bindOAuth records the table names the oauth module published to identity.
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

// oauthTable returns the physical name oauth published for a logical table.
func (t *foreignTables) oauthTable(logical string) (string, error) {
	if t == nil {
		return "", fmt.Errorf("%w: no published oauth tables", errRepositoryUnavailable)
	}
	return publishedTableName(t.oauth, "oauth", logical)
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
