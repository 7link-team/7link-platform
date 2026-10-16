package idempotency

import (
	goosex "7link.com/platform/pkg/extension/goose"
	"embed"
)

//go:embed sql/sqlite sql/mysql sql/postgres
var migrations embed.FS

func (*Module) MigrationSource() goosex.Source {
	return goosex.Source{FS: migrations, TableName: "goose_idempotency"}
}
