package query

import (
	"7link.com/platform/internal/planning/model"
	g "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct {
	GoalSnapshots                                                                                                      g.TableName
	HeavyRuns, Goals, Plans, Reports, ReportSubmissions, ReportChecks, Settings, LoopContracts, LoopRuns, Contributors g.TableName
}

func BindNames(prefix string) TableNames {
	return TableNames{GoalSnapshots: g.NewTableName(prefix, model.GoalSnapshot{}.TableName()), ReportChecks: g.NewTableName(prefix, model.ReportCheck{}.TableName()), HeavyRuns: g.NewTableName(prefix, model.HeavyRun{}.TableName()), Goals: g.NewTableName(prefix, model.Goal{}.TableName()), Plans: g.NewTableName(prefix, model.Plan{}.TableName()), Reports: g.NewTableName(prefix, model.Report{}.TableName()), ReportSubmissions: g.NewTableName(prefix, model.ReportSubmission{}.TableName()), Settings: g.NewTableName(prefix, model.Settings{}.TableName()), LoopContracts: g.NewTableName(prefix, model.LoopContract{}.TableName()), LoopRuns: g.NewTableName(prefix, model.LoopRun{}.TableName()), Contributors: g.NewTableName(prefix, model.Contributor{}.TableName())}
}
func BindReportSubmissions(db *gorm.DB, n TableNames) *reportSubmission {
	return Use(db).ReportSubmission.Table(n.ReportSubmissions.String())
}
func BindContributors(db *gorm.DB, n TableNames) *contributor {
	return Use(db).Contributor.Table(n.Contributors.String())
}
func BindLoopContracts(db *gorm.DB, n TableNames) *loopContract {
	return Use(db).LoopContract.Table(n.LoopContracts.String())
}
func BindLoopRuns(db *gorm.DB, n TableNames) *loopRun {
	return Use(db).LoopRun.Table(n.LoopRuns.String())
}
func BindSettings(db *gorm.DB, n TableNames) *settings {
	return Use(db).Settings.Table(n.Settings.String())
}
func BindGoals(db *gorm.DB, n TableNames) *goal     { return Use(db).Goal.Table(n.Goals.String()) }
func BindPlans(db *gorm.DB, n TableNames) *plan     { return Use(db).Plan.Table(n.Plans.String()) }
func BindReports(db *gorm.DB, n TableNames) *report { return Use(db).Report.Table(n.Reports.String()) }

func BindHeavyRuns(db *gorm.DB, n TableNames) *heavyRun {
	return Use(db).HeavyRun.Table(n.HeavyRuns.String())
}

func BindReportChecks(db *gorm.DB, n TableNames) *reportCheck {
	return Use(db).ReportCheck.Table(n.ReportChecks.String())
}

func BindGoalSnapshots(db *gorm.DB, n TableNames) *goalSnapshot {
	return Use(db).GoalSnapshot.Table(n.GoalSnapshots.String())
}
