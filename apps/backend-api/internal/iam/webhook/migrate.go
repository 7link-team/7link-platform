// Package webhook delivers a tenant's own events to endpoints it registers.
//
// It follows the tenant's audit chain rather than being told about each change:
// the chain is already the complete, ordered, durable record of what happened,
// so a follower with a cursor cannot miss an event and no other module has to
// remember to announce one. What leaves here carries identifiers and outcomes
// only -- audit's own projection withholds the three columns that hold a
// person's data, because an endpoint somebody else operates cannot be made to
// forget what it has been handed. Intake callbacks use a separate immutable
// metadata protocol that also excludes tenant, actor, sequence and outcome.
package webhook

//go:generate go run ./dbgen

import (
	"embed"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
)

const Name = "webhook"

func (*Module) ModuleName() string { return Name }

//go:embed sql
var migrationsFS embed.FS

func (*Module) MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_webhook"}
}

func (*Module) MigrationMilestones() []module.MigrationMilestone {
	return []module.MigrationMilestone{
		{Name: "base", Version: 1},
		{Name: "intake_metadata", Version: 2},
	}
}

var (
	_ module.MigrationSourceProvider    = (*Module)(nil)
	_ module.MigrationMilestoneProvider = (*Module)(nil)
)
