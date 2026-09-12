package provisioning

import (
	"fmt"

	gormx "7link.com/platform/pkg/extension/gorm"
)

// foreignTables carries the names of the tables provisioning reads but does
// not own. Every owner is activated before provisioning and publishes its set
// as a capability, which provisioning resolves during injection; a set that
// never arrived stays empty and every lookup fails, so a missing publication
// is an error rather than a name provisioning would otherwise have to invent
// from its own prefix.
type foreignTables struct {
	access    gormx.TableSet
	principal gormx.TableSet
	directory gormx.TableSet
}

// bind records what each owner published. It is what the module hands the
// repository once injection has resolved the three capabilities.
func (t *foreignTables) bind(access, principal, directory gormx.TableSet) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published tables", errRepositoryUnavailable)
	}
	t.access, t.principal, t.directory = access, principal, directory
	return nil
}

// BindForeignTables records the table-name sets of the tables this service
// reads but does not own. Module injection resolves them from the owners that
// published them; a caller that builds a service outside the module has to
// call it itself, because an unbound service reports its statements as
// unavailable rather than querying an empty table name.
func (s *Service) BindForeignTables(access, principal, directory gormx.TableSet) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("%w: no service to bind", errRepositoryUnavailable)
	}
	return s.repo.foreign.bind(access, principal, directory)
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
