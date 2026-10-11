package organization

//go:generate go run ./dbgen

import (
	"embed"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
)

const Name = "organization"

//go:embed sql
var migrationsFS embed.FS

// MigrationSource is this module's migration stream. It is a package function
// so principal erasure can read the stream's version while the module is
// disabled, without holding a Module.
func MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_organization"}
}

func (*Module) MigrationSource() goosex.Source { return MigrationSource() }

func (*Module) MigrationMilestones() []module.MigrationMilestone {
	return []module.MigrationMilestone{
		{Name: "base", Version: 7},
		{
			Name:    "constraints",
			Version: 8,
			Dependencies: []module.MigrationMilestoneRef{
				{Module: "iam", Name: "base"},
			},
		},
		{Name: "principal-erasure", Version: 9,
			Dependencies: []module.MigrationMilestoneRef{{Module: "identity", Name: "lifecycle"}}},
		{Name: "department-entities", Version: 10,
			Dependencies: []module.MigrationMilestoneRef{{Module: "iam", Name: "base"}}},
		{Name: "retire-positions", Version: 11},
		{Name: "administration-scopes", Version: 12},
	}
}

var (
	_ module.MigrationSourceProvider    = (*Module)(nil)
	_ module.MigrationMilestoneProvider = (*Module)(nil)
)
