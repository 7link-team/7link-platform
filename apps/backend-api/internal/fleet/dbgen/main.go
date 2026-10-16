package main

import (
	"7link.com/platform/internal/fleet/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() { gengorm.Generate(model.Agent{}, model.AgentRole{}) }
