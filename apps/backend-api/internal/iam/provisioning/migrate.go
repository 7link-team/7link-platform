// Package provisioning owns inbound and outbound SCIM persistence.
//
//go:generate go run ./dbgen
package provisioning

import (
	"embed"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
)

const Name = "provisioning"

func (*Module) ModuleName() string { return Name }

//go:embed sql
var migrationsFS embed.FS

func (*Module) MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_provisioning"}
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
		{
			Name: "service_accounts", Version: 4,
			Dependencies: []module.MigrationMilestoneRef{{Module: "identity", Name: "base"}},
		},
		{Name: "principal-erasure", Version: 5,
			Dependencies: []module.MigrationMilestoneRef{{Module: "identity", Name: "lifecycle"}}},
		{Name: "outbound-coordination", Version: 6},
	}
}

var (
	_ module.MigrationSourceProvider    = (*Module)(nil)
	_ module.MigrationMilestoneProvider = (*Module)(nil)
)
