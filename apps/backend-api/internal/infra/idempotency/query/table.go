package query

import (
	"7link.com/platform/internal/infra/idempotency/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type Keys = keyRow
type TableNames struct{ Keys gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{Keys: gormx.NewTableName(prefix, model.KeyRow{}.TableName())}
}
func Bind(db *gorm.DB, names TableNames) *Keys { return Use(db).KeyRow.Table(names.Keys.String()) }
