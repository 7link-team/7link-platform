package core

import (
	"embed"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
)

const Name = "iam"

func (*Module) ModuleName() string { return Name }

//go:embed sql
var migrationsFS embed.FS

// MigrationSource is this module's migration stream. It is a package function
// so principal erasure can read the stream's version while the module is
// disabled, without holding a Module.
func MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_iam"}
}

func (*Module) MigrationSource() goosex.Source { return MigrationSource() }

func (*Module) MigrationMilestones() []module.MigrationMilestone {
	return []module.MigrationMilestone{
		{Name: "base", Version: 11},
		{Name: "field_policies", Version: 12},
		{Name: "principal-erasure", Version: 13,
			Dependencies: []module.MigrationMilestoneRef{{Module: "identity", Name: "lifecycle"}}},
		{Name: "menu-translations", Version: 14},
		{Name: "default-menu-translations", Version: 15},
		{Name: "resource-menu-routes", Version: 16},
		{Name: "retire-positions", Version: 17,
			Dependencies: []module.MigrationMilestoneRef{{Module: "organization", Name: "retire-positions"}}},
	}
}

var (
	_ module.MigrationSourceProvider    = (*Module)(nil)
	_ module.MigrationMilestoneProvider = (*Module)(nil)
)
