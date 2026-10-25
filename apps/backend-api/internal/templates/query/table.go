package query

import (
	"7link.com/platform/internal/templates/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct{ Versions, Bindings, StudioRecords, LifecycleReceipts, CalibrationSamples, Calibrations, CalibrationRuns, EvalCases, EvalRuns, Providers, ProviderBindings, ProviderGenerations gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{Versions: gormx.NewTableName(prefix, model.Version{}.TableName()), Bindings: gormx.NewTableName(prefix, model.Binding{}.TableName()), StudioRecords: gormx.NewTableName(prefix, model.StudioRecord{}.TableName()), LifecycleReceipts: gormx.NewTableName(prefix, model.LifecycleReceipt{}.TableName()), CalibrationSamples: gormx.NewTableName(prefix, model.CalibrationSample{}.TableName()), Calibrations: gormx.NewTableName(prefix, model.Calibration{}.TableName()), CalibrationRuns: gormx.NewTableName(prefix, model.CalibrationRun{}.TableName()), EvalCases: gormx.NewTableName(prefix, model.EvalCase{}.TableName()), EvalRuns: gormx.NewTableName(prefix, model.EvalRun{}.TableName()), Providers: gormx.NewTableName(prefix, model.TemplateProvider{}.TableName()), ProviderBindings: gormx.NewTableName(prefix, model.TemplateProviderBinding{}.TableName()), ProviderGenerations: gormx.NewTableName(prefix, model.ProviderGeneration{}.TableName())}
}
func BindProviders(db *gorm.DB, n TableNames) *templateProvider {
	return Use(db).TemplateProvider.Table(n.Providers.String())
}
func BindProviderBindings(db *gorm.DB, n TableNames) *templateProviderBinding {
	return Use(db).TemplateProviderBinding.Table(n.ProviderBindings.String())
}
func BindProviderGenerations(db *gorm.DB, n TableNames) *providerGeneration {
	return Use(db).ProviderGeneration.Table(n.ProviderGenerations.String())
}
func BindCalibrationSamples(db *gorm.DB, n TableNames) *calibrationSample {
	return Use(db).CalibrationSample.Table(n.CalibrationSamples.String())
}
func BindCalibrations(db *gorm.DB, n TableNames) *calibration {
	return Use(db).Calibration.Table(n.Calibrations.String())
}
func BindCalibrationRuns(db *gorm.DB, n TableNames) *calibrationRun {
	return Use(db).CalibrationRun.Table(n.CalibrationRuns.String())
}
func BindVersions(db *gorm.DB, n TableNames) *version {
	return Use(db).Version.Table(n.Versions.String())
}
func BindBindings(db *gorm.DB, n TableNames) *binding {
	return Use(db).Binding.Table(n.Bindings.String())
}
func BindStudioRecords(db *gorm.DB, n TableNames) *studioRecord {
	return Use(db).StudioRecord.Table(n.StudioRecords.String())
}
func BindLifecycleReceipts(db *gorm.DB, n TableNames) *lifecycleReceipt {
	return Use(db).LifecycleReceipt.Table(n.LifecycleReceipts.String())
}
