// Package federation owns external identity providers, identity links, and SAML data.
package federation

//go:generate go run ./dbgen

import (
	"embed"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
)

const Name = "federation"

func (*Module) ModuleName() string { return Name }

//go:embed sql
var migrationsFS embed.FS

func (*Module) MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_federation"}
}

func (*Module) MigrationMilestones() []module.MigrationMilestone {
	return []module.MigrationMilestone{
		{Name: "base", Version: 2},
		{
			Name:    "constraints",
			Version: 3,
			Dependencies: []module.MigrationMilestoneRef{
				{Module: "identity", Name: "base"},
			},
		},
		{Name: "replay", Version: 4},
		{Name: "principal-erasure", Version: 5,
			Dependencies: []module.MigrationMilestoneRef{{Module: "identity", Name: "lifecycle"}}},
		{Name: "identity-link-transactions", Version: 6},
	}
}

var (
	_ module.MigrationSourceProvider    = (*Module)(nil)
	_ module.MigrationMilestoneProvider = (*Module)(nil)
)
