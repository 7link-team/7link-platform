// Package oauth owns OAuth clients, authorization codes, consents, and refresh tokens.
package oauth

//go:generate go run ./dbgen

import (
	"embed"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
)

const Name = "oauth"

//go:embed sql
var migrationsFS embed.FS

func (*Module) MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_oauth"}
}

func (*Module) MigrationMilestones() []module.MigrationMilestone {
	return []module.MigrationMilestone{
		{Name: "base", Version: 4},
		{
			Name:    "constraints",
			Version: 5,
			Dependencies: []module.MigrationMilestoneRef{
				{Module: "identity", Name: "base"},
				{Module: "organization", Name: "base"},
			},
		},
		{Name: "indexes", Version: 6},
	}
}

var (
	_ module.MigrationSourceProvider    = (*Module)(nil)
	_ module.MigrationMilestoneProvider = (*Module)(nil)
)
