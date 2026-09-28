package organization

import (
	"fmt"

	"7link.com/platform/internal/iam/core"
)

// foreignTables carries the names of the tables organization reads but does not
// own, in the form core publishes them.
//
// core is activated before organization and organization imports it, so core
// hands over a struct whose fields are the tables it owns rather than a set
// looked up by a string. A table organization asks for that core does not have
// is then a compile error rather than a statement that fails in the deployment
// that first runs it.
//
// Whether the names arrived is checked once, when the module initializes --
// the registry has run every Provide and Bind by then -- so an owner that
// published nothing fails startup rather than the first query that needs it.
type foreignTables struct {
	access core.AccessTableNames
}

// bind records the table names the core module published to organization.
// It is what the module hands the repositories once injection has resolved the
// capability.
func (t *foreignTables) bind(access core.AccessTableNames) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published access tables", errRepositoryUnavailable)
	}
	t.access = access
	return nil
}

// bound reports whether core published. Until it has, no statement that reads
// a table organization does not own may run.
func (t *foreignTables) bound() bool {
	return t != nil && t.access.Bound()
}
