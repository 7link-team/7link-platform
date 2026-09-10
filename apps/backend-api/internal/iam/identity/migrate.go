package identity

//go:generate go run ./dbgen

import (
	"embed"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
)

const Name = "identity"

func (*Module) ModuleName() string { return Name }

//go:embed sql
var migrationsFS embed.FS

func (*Module) MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_identity"}
}

func (*Module) MigrationMilestones() []module.MigrationMilestone {
	return []module.MigrationMilestone{{Name: "base", Version: 2}, {Name: "lifecycle", Version: 4}, {Name: "closure-requests", Version: 5}}
}

var (
	_ module.MigrationSourceProvider    = (*Module)(nil)
	_ module.MigrationMilestoneProvider = (*Module)(nil)
)
