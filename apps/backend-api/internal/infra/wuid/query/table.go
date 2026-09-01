package query

import (
	"gorm.io/gorm"

	"7link.com/platform/internal/infra/wuid/model"
)

// WorkerRows exports the generated query type. The generator names it after the
// model in lower case, which makes it unusable as a field or return type
// outside this package; the alias fixes that without hand-writing the type.
type WorkerRows = workerRow

// Bind returns the WorkerRow query bound to prefix + the model's table name. It
// is the single place the worker_ids table is named, so a prefixed deployment
// cannot end up half-applied.
func Bind(db *gorm.DB, prefix string) *WorkerRows {
	return Use(db).WorkerRow.Table(prefix + model.WorkerRow{}.TableName())
}
