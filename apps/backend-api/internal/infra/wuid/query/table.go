package query

import (
	"gorm.io/gorm"

	"7link.com/platform/internal/infra/wuid/model"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// WorkerRows exports the generated query type. The generator names it after the
// model in lower case, which makes it unusable as a field or return type
// outside this package; the alias fixes that without hand-writing the type.
type WorkerRows = workerRow

// TableNames is the physical name of every table infra/wuid owns under one
// deployment prefix.
type TableNames struct {
	WorkerRows gormx.TableName
}

// BindNames returns the physical name of every table infra/wuid owns. It is the
// only place the deployment prefix is applied to one of them, so a deployment
// cannot end up with half of infra/wuid's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		WorkerRows: gormx.NewTableName(prefix, model.WorkerRow{}.TableName()),
	}
}

// Bind returns the WorkerRow query bound to prefix + the model's table name. It
// builds from BindNames, so the prefix reaches the generated query object
// through one path.
func Bind(db *gorm.DB, prefix string) *WorkerRows {
	names := BindNames(prefix)
	return Use(db).WorkerRow.Table(names.WorkerRows.String())
}
