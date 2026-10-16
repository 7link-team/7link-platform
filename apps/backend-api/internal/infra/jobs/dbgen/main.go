package main

import (
	"7link.com/platform/internal/infra/jobs/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() { gengorm.Generate(model.Job{}, model.Schedule{}) }
