package main

import (
	"7link.com/platform/internal/notification/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.Notification{}, model.Preference{}, model.Delivery{}, model.RotationAlert{}, model.AgentError{}, model.DispatchReceipt{}, model.IntakeReceipt{}, model.SourceReceipt{})
}
