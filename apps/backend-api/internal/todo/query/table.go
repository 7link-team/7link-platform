package query

import (
	"gorm.io/gorm"

	"7link.com/platform/internal/todo/model"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// Todos exports the generated query type. The generator names it after the
// model in lower case, which makes it unusable as a field or return type
// outside this package; the alias fixes that without hand-writing the type.
type Todos = todoRow

// TableNames is the physical name of every table todo owns under one
// deployment prefix.
type TableNames struct {
	Todos gormx.TableName
}

// BindNames returns the physical name of every table todo owns. It is the only
// place the deployment prefix is applied to one of them, so a deployment
// cannot end up with half of todo's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		Todos: gormx.NewTableName(prefix, model.TodoRow{}.TableName()),
	}
}

// Bind returns the TodoRow query bound to prefix + the model's table name. It
// builds from BindNames, so the prefix reaches the generated query object
// through one path.
func Bind(db *gorm.DB, prefix string) *Todos {
	names := BindNames(prefix)
	return Use(db).TodoRow.Table(names.Todos.String())
}
