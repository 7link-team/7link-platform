package planning

import (
	g "7link.com/platform/pkg/extension/goose"
	"embed"
)

//go:embed sql/*/*.sql
var migrations embed.FS

func (*Module) MigrationSource() g.Source {
	return g.Source{FS: migrations, TableName: "goose_planning"}
}
