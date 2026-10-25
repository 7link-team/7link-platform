package main

import (
	"7link.com/platform/internal/templates/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.Version{}, model.Binding{}, model.StudioRecord{}, model.LifecycleReceipt{}, model.CalibrationSample{}, model.Calibration{}, model.CalibrationRun{}, model.EvalCase{}, model.EvalRun{}, model.TemplateProvider{}, model.TemplateProviderBinding{}, model.ProviderGeneration{})
}
