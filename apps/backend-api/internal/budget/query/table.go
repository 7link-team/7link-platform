package query

import (
	"7link.com/platform/internal/budget/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct{ Reservations gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{Reservations: gormx.NewTableName(prefix, model.Reservation{}.TableName())}
}

func BindReservations(db *gorm.DB, names TableNames) *reservation {
	return Use(db).Reservation.Table(names.Reservations.String())
}
