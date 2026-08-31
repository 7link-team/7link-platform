package audit

import (
	"embed"

	goosex "7link.com/platform/pkg/extension/goose"
)

//go:embed sql/sqlite sql/mysql sql/postgres
var migrationsFS embed.FS

// MigrationSource returns the audit module's migration source.
func (*Module) MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_audit"}
}
