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

// PrincipalTables is how identity hands its table names to the modules that
// JOIN them. Those modules are activated after identity, so they declare
// identity as a dependency and resolve the published set during their own
// injection, rather than applying the deployment prefix to a name they do not
// own.
type PrincipalTables struct {
	Published gormx.TableSet
}
