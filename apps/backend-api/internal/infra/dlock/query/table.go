package query

import (
	"gorm.io/gorm"

	"7link.com/platform/internal/infra/dlock/model"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// LockRows exports the generated query type. The generator names it after the
// model in lower case, which makes it unusable as a field or return type
// outside this package; the alias fixes that without hand-writing the type.
type LockRows = lockRow

// TableNames is the physical name of every table infra/dlock owns under one
// deployment prefix.
type TableNames struct {
	LockRows gormx.TableName
}

// BindNames returns the physical name of every table infra/dlock owns. It is
// the only place the deployment prefix is applied to one of them, so a
// deployment cannot end up with half of infra/dlock's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		LockRows: gormx.NewTableName(prefix, model.LockRow{}.TableName()),
	}
}

// Map indexes the names by logical name so the module root can publish them as
// a set to the modules that JOIN infra/dlock's table.
func (names TableNames) Map() map[string]gormx.TableName {
	return map[string]gormx.TableName{
		model.LockRow{}.TableName(): names.LockRows,
	}
}

// Bind returns the LockRow query bound to prefix + the model's table name. It
// builds from BindNames, so the prefix reaches the generated query object
// through one path.
func Bind(db *gorm.DB, prefix string) *LockRows {
	names := BindNames(prefix)
	return Use(db).LockRow.Table(names.LockRows.String())
}
