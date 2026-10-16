package governance

import (
	goosex "7link.com/platform/pkg/extension/goose"
	"embed"
)

//go:embed sql/sqlite sql/mysql sql/postgres
var migrationsFS embed.FS

func (*Module) MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_business_governance"}
}
