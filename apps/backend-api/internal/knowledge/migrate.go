package knowledge

import (
	goosex "7link.com/platform/pkg/extension/goose"
	"embed"
)

//go:embed sql/*/*.sql
var migrationsFS embed.FS

func (*Module) MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_knowledge"}
}
