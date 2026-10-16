package main

import (
	"7link.com/platform/internal/intake/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() { gengorm.Generate(model.Contact{}, model.Record{}) }
