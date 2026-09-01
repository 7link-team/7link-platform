package query

import (
	"gorm.io/gorm"

	"7link.com/platform/internal/todo/model"
)

// Todos exports the generated query type. The generator names it after the
// model in lower case, which makes it unusable as a field or return type
// outside this package; the alias fixes that without hand-writing the type.
type Todos = todoRow

// Bind returns the TodoRow query bound to prefix + the model's table name. It
// is the single place the todos table is named, so a prefixed deployment cannot
// end up half-applied.
func Bind(db *gorm.DB, prefix string) *Todos {
	return Use(db).TodoRow.Table(prefix + model.TodoRow{}.TableName())
}
