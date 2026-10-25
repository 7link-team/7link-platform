package main

import (
	"7link.com/platform/internal/infra/events/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() { gengorm.Generate(model.EventRow{}, model.Cursor{}, model.Receipt{}, model.Parked{}) }
