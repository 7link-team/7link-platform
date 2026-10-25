package query

import (
	"gorm.io/gorm"
)

func BindDeliveryConfirmations(db *gorm.DB, names TableNames) *deliveryConfirmation {
	return Use(db).DeliveryConfirmation.Table(names.DeliveryConfirmations.String())
}
