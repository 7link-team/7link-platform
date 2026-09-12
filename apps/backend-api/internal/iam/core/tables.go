package core

import (
	"7link.com/platform/internal/iam/core/query"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// TableSet publishes the physical names of the tables core owns to the modules
// that JOIN them.
//
// It carries names rather than a database handle or a prefix: a consumer
// resolves the names it needs and queries with its own connection, so it cannot
// build a name core never published.
func TableSet(prefix string) gormx.TableSet {
	return gormx.NewTableSet(query.BindNames(prefix).Map())
}

// AccessTables is how core hands its table names to the modules that JOIN
// them. Those modules are activated after core, so they declare core as a
// dependency and resolve the published set during their own injection, rather
// than applying the deployment prefix to a name they do not own.
type AccessTables struct {
	Published gormx.TableSet
}
