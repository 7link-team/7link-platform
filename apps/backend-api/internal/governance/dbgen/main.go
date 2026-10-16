package main

import (
	"7link.com/platform/internal/governance/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() { gengorm.Generate(model.RoleSeed{}) }
