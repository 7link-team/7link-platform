package query

import (
	"gorm.io/gorm"

	"7link.com/platform/internal/iam/webhook/model"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// Subscriptions and Deliveries export the generated query types. The generator
// names them after the models in lower case, which makes them unusable as field
// or return types outside this package; the aliases fix that without
// hand-writing the types.
type (
	Subscriptions = subscription
	Deliveries    = delivery
)

// TableNames is the physical name of every table webhook owns under one
// deployment prefix.
type TableNames struct {
	Subscriptions gormx.TableName
	Deliveries    gormx.TableName
}

// BindNames returns the physical name of every table webhook owns. It is the
// only place the deployment prefix is applied to one of them, so a deployment
// cannot end up with half of webhook's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		Subscriptions: gormx.NewTableName(prefix, model.Subscription{}.TableName()),
		Deliveries:    gormx.NewTableName(prefix, model.Delivery{}.TableName()),
	}
}

// Bind returns the query set bound to the prefixed table names, so the prefix
// reaches the generated query objects through one path.
func Bind(db *gorm.DB, prefix string) (*Subscriptions, *Deliveries) {
	names := BindNames(prefix)
	use := Use(db)
	return use.Subscription.Table(names.Subscriptions.String()),
		use.Delivery.Table(names.Deliveries.String())
}
