package query

import (
	"gorm.io/gorm"

	"7link.com/platform/internal/infra/audit/model"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// OperationLogs exports the generated query type. The generator names it after
// the model in lower case, which makes it unusable as a field or return type
// outside this package; the alias fixes that without hand-writing the type.
type OperationLogs = operationLog

// TableNames is the physical name of every table infra/audit owns under one
// deployment prefix.
type TableNames struct {
	OperationLogs gormx.TableName
}

// BindNames returns the physical name of every table infra/audit owns. It is
// the only place the deployment prefix is applied to one of them, so a
// deployment cannot end up with half of infra/audit's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		OperationLogs: gormx.NewTableName(prefix, model.OperationLog{}.TableName()),
	}
}

// Map indexes the names by logical name so the module root can publish them as
// a set to the modules that JOIN infra/audit's table.
func (names TableNames) Map() map[string]gormx.TableName {
	return map[string]gormx.TableName{
		model.OperationLog{}.TableName(): names.OperationLogs,
	}
}

// Bind returns the OperationLog query bound to prefix + the model's table name.
// It builds from BindNames, so the prefix reaches the generated query object
// through one path.
func Bind(db *gorm.DB, prefix string) *OperationLogs {
	names := BindNames(prefix)
	return Use(db).OperationLog.Table(names.OperationLogs.String())
}
