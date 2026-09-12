package oauth

import (
	"fmt"

	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
)

// foreignTables carries the names of the tables oauth reads but does not own,
// in the form each owner publishes them.
//
// Every owner is activated before oauth and oauth imports all four, so each
// hands over a struct whose fields are the tables it owns rather than a set
// looked up by a string. A table oauth asks for that its owner does not have is
// then a compile error rather than a statement that fails in the deployment
// that first runs it.
//
// Whether the names arrived is checked once, when the module initializes -- the
// registry has run every Provide and Bind by then -- so an owner that published
// nothing fails startup rather than the first query that needs it.
type foreignTables struct {
	access     core.AccessTableNames
	principal  identity.PrincipalTableNames
	credential authn.CredentialTableNames
	directory  organization.DirectoryTableNames
}

// bind records what each owner published. It is what the module hands the
// repository once injection has resolved the four capabilities.
func (t *foreignTables) bind(
	access core.AccessTableNames, principal identity.PrincipalTableNames,
	credential authn.CredentialTableNames, directory organization.DirectoryTableNames,
) error {
	if t == nil {
		return fmt.Errorf("%w: no holder for the published tables", errRepositoryUnavailable)
	}
	t.access, t.principal = access, principal
	t.credential, t.directory = credential, directory
	return nil
}

// bound reports whether every owner published. Until they all have, no
// statement that reads a table oauth does not own may run.
func (t *foreignTables) bound() bool {
	return t != nil && t.access.Bound() && t.principal.Bound() &&
		t.credential.Bound() && t.directory.Bound()
}
