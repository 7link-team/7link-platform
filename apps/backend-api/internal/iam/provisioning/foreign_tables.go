package provisioning

import (
	"fmt"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
)

// foreignTables carries the names of the tables provisioning reads but does
// not own, in the form each owner publishes them.
//
// Every owner is activated before provisioning and provisioning imports all
// three, so each hands over a struct whose fields are the tables it owns
// rather than a set looked up by a string. A table provisioning asks for that
// its owner does not have is then a compile error rather than a statement that
// fails in the deployment that first runs it.
//
// Whether the names arrived is checked once, when the module initializes --
// the registry has run every Provide and Bind by then -- so an owner that
// published nothing fails startup rather than the first query that needs it.
type foreignTables struct {
	access    core.AccessTableNames
	principal identity.PrincipalTableNames
	directory organization.DirectoryTableNames
}

// bind records what each owner published. It is what the module hands the
// repository once injection has resolved the three capabilities.
func (t *foreignTables) bind(access core.AccessTableNames, principal identity.PrincipalTableNames, directory organization.DirectoryTableNames) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published tables", errRepositoryUnavailable)
	}
	t.access, t.principal, t.directory = access, principal, directory
	return nil
}

// bound reports whether every owner published. Until they all have, no
// statement that reads a table provisioning does not own may run.
func (t *foreignTables) bound() bool {
	return t != nil && t.access.Bound() && t.principal.Bound() && t.directory.Bound()
}

// BindForeignTables records the names of the tables this service reads but
// does not own. Module injection resolves them from the owners that published
// them and Initialize refuses to start a deployment whose owners published
// nothing; a caller that builds a service outside the module has to call it
// itself, because nothing else fills the names its statements join.
func (s *Service) BindForeignTables(access core.AccessTableNames, principal identity.PrincipalTableNames, directory organization.DirectoryTableNames) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("%w: no service to bind", errRepositoryUnavailable)
	}
	return s.repo.foreign.bind(access, principal, directory)
}
