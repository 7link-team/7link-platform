package authn

//go:generate go run ./dbgen

import (
	"embed"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
)

const Name = "authn"

func (*Module) ModuleName() string { return Name }

//go:embed sql
var migrationsFS embed.FS

func (*Module) MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_authn"}
}

func (*Module) MigrationMilestones() []module.MigrationMilestone {
	return []module.MigrationMilestone{
		{Name: "base", Version: 8},
		{
			Name:    "constraints",
			Version: 9,
			Dependencies: []module.MigrationMilestoneRef{
				{Module: "identity", Name: "base"},
			},
		},
		{Name: "indexes", Version: 10},
		{Name: "verification-attempts", Version: 11},
		{Name: "notification-principals", Version: 12},
		{Name: "principal-erasure", Version: 13,
			Dependencies: []module.MigrationMilestoneRef{{Module: "identity", Name: "lifecycle"}}},
		{Name: "reauthentication", Version: 14},
		{Name: "personal-tokens", Version: 15, Dependencies: []module.MigrationMilestoneRef{{Module: "organization", Name: "base"}}},
		{Name: "account-view-tokens", Version: 16},
		{Name: "access-view-targets", Version: 17},
	}
}

var (
	_ module.MigrationSourceProvider    = (*Module)(nil)
	_ module.MigrationMilestoneProvider = (*Module)(nil)
)
