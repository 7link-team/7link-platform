// Package governance owns access requests and access reviews.
package governance

//go:generate go run ./dbgen

import (
	"embed"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
)

const Name = "governance"

func (*Module) ModuleName() string { return Name }

//go:embed sql
var migrationsFS embed.FS

func (*Module) MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_governance"}
}

func (*Module) MigrationMilestones() []module.MigrationMilestone {
	return []module.MigrationMilestone{
		{Name: "base", Version: 2},
		{
			Name:    "constraints",
			Version: 3,
			Dependencies: []module.MigrationMilestoneRef{
				{Module: "organization", Name: "base"},
			},
		},
		{Name: "principal-erasure", Version: 4,
			Dependencies: []module.MigrationMilestoneRef{{Module: "identity", Name: "lifecycle"}}},
		{Name: "retire-positions", Version: 5,
			Dependencies: []module.MigrationMilestoneRef{{Module: "organization", Name: "retire-positions"}}},
	}
}

var (
	_ module.MigrationSourceProvider    = (*Module)(nil)
	_ module.MigrationMilestoneProvider = (*Module)(nil)
)
