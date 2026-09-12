package organization

import (
	"fmt"

	gormx "7link.com/platform/pkg/extension/gorm"
)

// foreignTables carries the names of the tables organization reads but does not
// own. core owns them and is activated before organization, so it publishes
// them as an ordinary capability; a set that never arrived stays empty and
// every lookup fails, which is what keeps a missing publication from becoming
// an empty table name in a statement.
type foreignTables struct {
	access gormx.TableSet
}

// bindAccess records the table names the core module published to organization.
func (t *foreignTables) bindAccess(published gormx.TableSet) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published access tables", errRepositoryUnavailable)
	}
	t.access = published
	return nil
}

// accessTable returns the physical name core published for a logical table. An
// empty set means core has published nothing, and a missing name means it
// published something incomplete; both are errors rather than a table name
// organization would otherwise have to invent from its own prefix.
func (t *foreignTables) accessTable(logical string) (string, error) {
	if t == nil {
		return "", fmt.Errorf("%w: no published access tables", errRepositoryUnavailable)
	}
	if t.access.Len() == 0 {
		return "", fmt.Errorf("core has not published its table names: %w", errRepositoryUnavailable)
	}
	name, ok := t.access.Lookup(logical)
	if !ok {
		return "", fmt.Errorf("core did not publish %s: %w", logical, errRepositoryUnavailable)
	}
	return name.String(), nil
}
