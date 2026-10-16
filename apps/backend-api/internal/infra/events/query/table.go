package query

import (
	"7link.com/platform/internal/infra/events/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"context"
	"gorm.io/gorm"
)

type Events = eventRow
type TableNames struct{ Events gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{Events: gormx.NewTableName(prefix, model.EventRow{}.TableName())}
}
func BindWithNames(db *gorm.DB, names TableNames) *Events {
	return Use(db).EventRow.Table(names.Events.String())
}

// Notify wakes a PostgreSQL sequencer only after the caller commits. Other
// engines use polling. The channel uses the same resolved deployment name.
func Notify(ctx context.Context, tx *gorm.DB, names TableNames) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	return tx.WithContext(ctx).Exec("SELECT pg_notify(?, '')", names.Events.String()).Error
}
