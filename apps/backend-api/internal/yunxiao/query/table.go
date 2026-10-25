package query

import (
	"7link.com/platform/internal/yunxiao/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct {
	IngressObservations gormx.TableName
	WorklogRevisions    gormx.TableName
	Iterations          gormx.TableName
	Versions            gormx.TableName
	Milestones          gormx.TableName
	WorkPlanning        gormx.TableName
	PlanningCarryovers  gormx.TableName
	IntakeChannels      gormx.TableName
	Worklogs            gormx.TableName
	WorkflowConfigs     gormx.TableName
	Connections         gormx.TableName
	ProjectMappings     gormx.TableName
	ExternalRefs        gormx.TableName
	StateMappings       gormx.TableName
	ImportJobs          gormx.TableName
	InboundEvents       gormx.TableName
	SyncLogs            gormx.TableName
	AutomationRules     gormx.TableName
	AutomationRuns      gormx.TableName
}

func BindNames(prefix string) TableNames {
	return TableNames{IngressObservations: gormx.NewTableName(prefix, model.IngressObservation{}.TableName()), WorklogRevisions: gormx.NewTableName(prefix, model.WorklogRevision{}.TableName()), PlanningCarryovers: gormx.NewTableName(prefix, model.PlanningCarryover{}.TableName()), Iterations: gormx.NewTableName(prefix, model.Iteration{}.TableName()), Versions: gormx.NewTableName(prefix, model.PlanningVersion{}.TableName()), Milestones: gormx.NewTableName(prefix, model.Milestone{}.TableName()), WorkPlanning: gormx.NewTableName(prefix, model.WorkPlanning{}.TableName()), AutomationRules: gormx.NewTableName(prefix, model.AutomationRule{}.TableName()), AutomationRuns: gormx.NewTableName(prefix, model.AutomationRun{}.TableName()), IntakeChannels: gormx.NewTableName(prefix, model.IntakeChannel{}.TableName()), Worklogs: gormx.NewTableName(prefix, model.Worklog{}.TableName()), WorkflowConfigs: gormx.NewTableName(prefix, model.WorkflowConfig{}.TableName()), Connections: gormx.NewTableName(prefix, model.Connection{}.TableName()), ProjectMappings: gormx.NewTableName(prefix, model.ProjectMapping{}.TableName()), ExternalRefs: gormx.NewTableName(prefix, model.ExternalRef{}.TableName()), StateMappings: gormx.NewTableName(prefix, model.StateMapping{}.TableName()), ImportJobs: gormx.NewTableName(prefix, model.ImportJob{}.TableName()), InboundEvents: gormx.NewTableName(prefix, model.InboundEvent{}.TableName()), SyncLogs: gormx.NewTableName(prefix, model.SyncLog{}.TableName())}
}
func BindConnections(db *gorm.DB, n TableNames) *connection {
	return Use(db).Connection.Table(n.Connections.String())
}
func BindProjectMappings(db *gorm.DB, n TableNames) *projectMapping {
	return Use(db).ProjectMapping.Table(n.ProjectMappings.String())
}
func BindExternalRefs(db *gorm.DB, n TableNames) *externalRef {
	return Use(db).ExternalRef.Table(n.ExternalRefs.String())
}
func BindStateMappings(db *gorm.DB, n TableNames) *stateMapping {
	return Use(db).StateMapping.Table(n.StateMappings.String())
}
func BindImportJobs(db *gorm.DB, n TableNames) *importJob {
	return Use(db).ImportJob.Table(n.ImportJobs.String())
}
func BindInboundEvents(db *gorm.DB, n TableNames) *inboundEvent {
	return Use(db).InboundEvent.Table(n.InboundEvents.String())
}
func BindSyncLogs(db *gorm.DB, n TableNames) *syncLog {
	return Use(db).SyncLog.Table(n.SyncLogs.String())
}

func BindWorklogs(db *gorm.DB, n TableNames) *worklog {
	return Use(db).Worklog.Table(n.Worklogs.String())
}
func BindWorkflowConfigs(db *gorm.DB, n TableNames) *workflowConfig {
	return Use(db).WorkflowConfig.Table(n.WorkflowConfigs.String())
}

func BindIntakeChannels(db *gorm.DB, n TableNames) *intakeChannel {
	return Use(db).IntakeChannel.Table(n.IntakeChannels.String())
}

func BindAutomationRules(db *gorm.DB, n TableNames) *automationRule {
	return Use(db).AutomationRule.Table(n.AutomationRules.String())
}
func BindAutomationRuns(db *gorm.DB, n TableNames) *automationRun {
	return Use(db).AutomationRun.Table(n.AutomationRuns.String())
}

func BindIterations(db *gorm.DB, n TableNames) *iteration {
	return Use(db).Iteration.Table(n.Iterations.String())
}
func BindVersions(db *gorm.DB, n TableNames) *planningVersion {
	return Use(db).PlanningVersion.Table(n.Versions.String())
}
func BindMilestones(db *gorm.DB, n TableNames) *milestone {
	return Use(db).Milestone.Table(n.Milestones.String())
}
func BindWorkPlanning(db *gorm.DB, n TableNames) *workPlanning {
	return Use(db).WorkPlanning.Table(n.WorkPlanning.String())
}
func BindPlanningCarryovers(db *gorm.DB, n TableNames) *planningCarryover {
	return Use(db).PlanningCarryover.Table(n.PlanningCarryovers.String())
}

func BindWorklogRevisions(db *gorm.DB, n TableNames) *worklogRevision {
	return Use(db).WorklogRevision.Table(n.WorklogRevisions.String())
}

func BindIngressObservations(db *gorm.DB, n TableNames) *ingressObservation {
	return Use(db).IngressObservation.Table(n.IngressObservations.String())
}
