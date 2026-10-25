package query

import (
	"7link.com/platform/internal/notification/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct{ Notifications, Preferences, Deliveries, RotationAlerts, AgentErrors, DispatchReceipts, IntakeReceipts, SourceReceipts gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{gormx.NewTableName(prefix, model.Notification{}.TableName()), gormx.NewTableName(prefix, model.Preference{}.TableName()), gormx.NewTableName(prefix, model.Delivery{}.TableName()), gormx.NewTableName(prefix, model.RotationAlert{}.TableName()), gormx.NewTableName(prefix, model.AgentError{}.TableName()), gormx.NewTableName(prefix, model.DispatchReceipt{}.TableName()), gormx.NewTableName(prefix, model.IntakeReceipt{}.TableName()), gormx.NewTableName(prefix, model.SourceReceipt{}.TableName())}
}
func BindNotifications(db *gorm.DB, n TableNames) *notification {
	return Use(db).Notification.Table(n.Notifications.String())
}
func BindPreferences(db *gorm.DB, n TableNames) *preference {
	return Use(db).Preference.Table(n.Preferences.String())
}

func BindDeliveries(db *gorm.DB, n TableNames) *delivery {
	return Use(db).Delivery.Table(n.Deliveries.String())
}

func BindRotationAlerts(db *gorm.DB, n TableNames) *rotationAlert {
	return Use(db).RotationAlert.Table(n.RotationAlerts.String())
}

func BindAgentErrors(db *gorm.DB, n TableNames) *agentError {
	return Use(db).AgentError.Table(n.AgentErrors.String())
}

func BindDispatchReceipts(db *gorm.DB, n TableNames) *dispatchReceipt {
	return Use(db).DispatchReceipt.Table(n.DispatchReceipts.String())
}

func BindIntakeReceipts(db *gorm.DB, n TableNames) *intakeReceipt {
	return Use(db).IntakeReceipt.Table(n.IntakeReceipts.String())
}

func BindSourceReceipts(db *gorm.DB, n TableNames) *sourceReceipt {
	return Use(db).SourceReceipt.Table(n.SourceReceipts.String())
}
