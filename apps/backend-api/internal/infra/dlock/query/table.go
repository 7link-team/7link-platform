package query

import (
	"gorm.io/gorm"

	"7link.com/platform/internal/infra/dlock/model"
)

// LockRows exports the generated query type. The generator names it after the
// model in lower case, which makes it unusable as a field or return type
// outside this package; the alias fixes that without hand-writing the type.
type LockRows = lockRow

// Bind returns the LockRow query bound to prefix + the model's table name. It
// is the single place the dlocks table is named, so a prefixed deployment
// cannot end up half-applied.
func Bind(db *gorm.DB, prefix string) *LockRows {
	return Use(db).LockRow.Table(prefix + model.LockRow{}.TableName())
}
