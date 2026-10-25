package notification

import (
	goosex "7link.com/platform/pkg/extension/goose"
	"embed"
)

//go:embed sql/sqlite sql/mysql sql/postgres
var migrationsFS embed.FS

func (*Module) MigrationSource() goosex.Source {
	return MigrationSource()
}

// MigrationSource permits lifecycle erasure to inspect retained schemas even
// when the notification module is disabled in the current deployment.
func MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_notification"}
}
