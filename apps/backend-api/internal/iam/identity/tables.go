package identity

import (
	"7link.com/platform/internal/iam/identity/query"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// TableSet publishes the physical names of the tables identity owns to the
// modules that JOIN them.
//
// It carries names rather than a database handle or a prefix: a consumer
// resolves the names it needs and queries with its own connection, so it cannot
// build a name identity never published.
func TableSet(prefix string) gormx.TableSet {
	return gormx.NewTableSet(query.BindNames(prefix).Map())
}

// PrincipalTableNames is the physical name of every table identity owns, as identity hands
// them to the modules that JOIN them.
//
// The fields are named, not looked up by string, so a consumer cannot ask for a
// table identity does not have and cannot misspell the one it wants.
type PrincipalTableNames struct {
	ClosureRequests           gormx.TableName
	Principals                gormx.TableName
	PrincipalLifecycles       gormx.TableName
	ServiceAccounts           gormx.TableName
	ServiceAccountCredentials gormx.TableName
}

// PublishedPrincipalTableNames binds every name identity owns to one deployment prefix. It
// builds from the module's own BindNames, so the prefix is applied the same way
// it is everywhere else identity queries, and a table added to identity cannot be
// forgotten here without the drift test noticing.
func PublishedPrincipalTableNames(prefix string) PrincipalTableNames {
	names := query.BindNames(prefix)
	return PrincipalTableNames{
		ClosureRequests:           names.ClosureRequests,
		Principals:                names.Principals,
		PrincipalLifecycles:       names.PrincipalLifecycles,
		ServiceAccounts:           names.ServiceAccounts,
		ServiceAccountCredentials: names.ServiceAccountCredentials,
	}
}

// Bound reports whether these names arrived. PublishedPrincipalTableNames fills every field
// at once, so one zero field means the consumer was never given the set rather
// than that identity published an incomplete one.
func (t PrincipalTableNames) Bound() bool { return !t.Principals.IsZero() }

// PrincipalTables is the opaque form of the same publication, still resolved by the
// consumers that have not moved to the typed names yet. It goes away with the
// last of them.
type PrincipalTables struct {
	Published gormx.TableSet
}
