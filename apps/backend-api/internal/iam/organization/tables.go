package organization

import (
	"7link.com/platform/internal/iam/organization/query"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// TableSet publishes the physical names of the tables organization owns to the
// modules that JOIN them.
//
// It carries names rather than a database handle or a prefix: a consumer
// resolves the names it needs and queries with its own connection, so it cannot
// build a name organization never published.
func TableSet(prefix string) gormx.TableSet {
	return gormx.NewTableSet(query.BindNames(prefix).Map())
}

// DirectoryTables is how organization hands its table names to the modules
// that JOIN them. Those modules are activated after organization, so they
// declare organization as a dependency and resolve the published set during
// their own injection, rather than applying the deployment prefix to a name
// they do not own.
type DirectoryTables struct {
	Published gormx.TableSet
}
