package query

import (
	"7link.com/platform/internal/performance/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct{ Schemes, Revisions, Reports, ReportRevisions gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{Schemes: gormx.NewTableName(prefix, model.Scheme{}.TableName()), Revisions: gormx.NewTableName(prefix, model.Revision{}.TableName()), Reports: gormx.NewTableName(prefix, model.Report{}.TableName()), ReportRevisions: gormx.NewTableName(prefix, model.ReportRevision{}.TableName())}
}
func BindSchemes(db *gorm.DB, n TableNames) *scheme { return Use(db).Scheme.Table(n.Schemes.String()) }
func BindRevisions(db *gorm.DB, n TableNames) *revision {
	return Use(db).Revision.Table(n.Revisions.String())
}
func BindReports(db *gorm.DB, n TableNames) *report { return Use(db).Report.Table(n.Reports.String()) }
func BindReportRevisions(db *gorm.DB, n TableNames) *reportRevision {
	return Use(db).ReportRevision.Table(n.ReportRevisions.String())
}
