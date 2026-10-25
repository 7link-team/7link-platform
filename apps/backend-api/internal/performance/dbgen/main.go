package main

import (
	"7link.com/platform/internal/performance/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.Scheme{}, model.Revision{}, model.Report{}, model.ReportRevision{})
}
