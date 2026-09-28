package federation

import (
	"fmt"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization"
)

// foreignTables carries the names of the tables federation reads but does not
// own, in the form each owner publishes them.
//
// Both owners are activated before federation and federation imports both, so
// each hands over a struct whose fields are the tables it owns rather than a
// set looked up by a string. A table federation asks for that its owner does
// not have is then a compile error rather than a statement that fails in the
// deployment that first runs it.
//
// Whether the names arrived is checked once, when the module initializes --
// the registry has run every Provide and Bind by then -- so an owner that
// published nothing fails startup rather than the first query that needs it.
type foreignTables struct {
	access    core.AccessTableNames
	directory organization.DirectoryTableNames
}

// bind records what each owner published. It is what the module hands the
// repository once injection has resolved the two capabilities.
func (t *foreignTables) bind(access core.AccessTableNames, directory organization.DirectoryTableNames) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published tables", errRepositoryUnavailable)
	}
	t.access, t.directory = access, directory
	return nil
}

// bound reports whether both owners published. Until they have, no statement
// that reads a table federation does not own may run.
func (t *foreignTables) bound() bool {
	return t != nil && t.access.Bound() && t.directory.Bound()
}
