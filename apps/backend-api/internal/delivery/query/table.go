package query

import (
	"7link.com/platform/internal/delivery/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

// TableNames holds the names resolved once for this deployment.
type TableNames struct {
	Deployments                                                                                                                                                                                                                                                                                                                                                                                                                                       gormx.TableName
	BudgetEvents                                                                                                                                                                                                                                                                                                                                                                                                                                      gormx.TableName
	AttentionSessions                                                                                                                                                                                                                                                                                                                                                                                                                                 gormx.TableName
	DeliveryConfirmations                                                                                                                                                                                                                                                                                                                                                                                                                             gormx.TableName
	ModeChanges                                                                                                                                                                                                                                                                                                                                                                                                                                       gormx.TableName
	ClarificationReceipts                                                                                                                                                                                                                                                                                                                                                                                                                             gormx.TableName
	CommentDefects                                                                                                                                                                                                                                                                                                                                                                                                                                    gormx.TableName
	ReportQualityReceipts, ReportQualityObservations                                                                                                                                                                                                                                                                                                                                                                                                  gormx.TableName
	CodeLinks, CodeLinkScans                                                                                                                                                                                                                                                                                                                                                                                                                          gormx.TableName
	WorkActivities                                                                                                                                                                                                                                                                                                                                                                                                                                    gormx.TableName
	ArtifactConsumptions                                                                                                                                                                                                                                                                                                                                                                                                                              gormx.TableName
	ReviewTypes, ReviewOpinions, ReviewSheets, ReviewPlans, Projects, Contacts, Repositories, Counters, Requirements, Criteria, RequirementVersions, WorkItems, Deliveries, WorkCriteria, WorkContracts, ReadinessChecks, Dependencies, WorkTransitions, Constitutions, Assignments, StageTransitions, ResultVersions, ResultEvidence, ResultActions, ResultHandoffs, Changes, ChangeImpacts, ResultFeedback, VerificationRuns, VerificationOverrides gormx.TableName
}

func BindNames(prefix string) TableNames {
	return TableNames{Deployments: gormx.NewTableName(prefix, model.Deployment{}.TableName()), BudgetEvents: gormx.NewTableName(prefix, model.ProjectBudgetEvent{}.TableName()), AttentionSessions: gormx.NewTableName(prefix, model.AttentionSession{}.TableName()), DeliveryConfirmations: gormx.NewTableName(prefix, model.DeliveryConfirmation{}.TableName()), ModeChanges: gormx.NewTableName(prefix, model.ProjectModeChange{}.TableName()), ClarificationReceipts: gormx.NewTableName(prefix, model.ClarificationReceipt{}.TableName()), CommentDefects: gormx.NewTableName(prefix, model.CommentDefect{}.TableName()), ReportQualityReceipts: gormx.NewTableName(prefix, model.ReportQualityReceipt{}.TableName()), ReportQualityObservations: gormx.NewTableName(prefix, model.ReportQualityObservation{}.TableName()), CodeLinks: gormx.NewTableName(prefix, model.CodeLink{}.TableName()), CodeLinkScans: gormx.NewTableName(prefix, model.CodeLinkScan{}.TableName()), WorkActivities: gormx.NewTableName(prefix, model.WorkItemActivity{}.TableName()), ArtifactConsumptions: gormx.NewTableName(prefix, model.ArtifactConsumption{}.TableName()), ReviewTypes: gormx.NewTableName(prefix, model.ReviewType{}.TableName()), ReviewOpinions: gormx.NewTableName(prefix, model.ReviewOpinion{}.TableName()), ReviewSheets: gormx.NewTableName(prefix, model.ReviewSheet{}.TableName()), ReviewPlans: gormx.NewTableName(prefix, model.ReviewPlan{}.TableName()), VerificationRuns: gormx.NewTableName(prefix, model.VerificationRun{}.TableName()), VerificationOverrides: gormx.NewTableName(prefix, model.VerificationOverride{}.TableName()), ResultVersions: gormx.NewTableName(prefix, model.ResultVersion{}.TableName()), ResultEvidence: gormx.NewTableName(prefix, model.ResultEvidence{}.TableName()), ResultActions: gormx.NewTableName(prefix, model.ResultAction{}.TableName()), ResultHandoffs: gormx.NewTableName(prefix, model.ResultHandoff{}.TableName()), Changes: gormx.NewTableName(prefix, model.ChangeRecord{}.TableName()), ChangeImpacts: gormx.NewTableName(prefix, model.ChangeImpact{}.TableName()), ResultFeedback: gormx.NewTableName(prefix, model.ResultFeedback{}.TableName()), StageTransitions: gormx.NewTableName(prefix, model.ProjectStageTransition{}.TableName()), Assignments: gormx.NewTableName(prefix, model.Assignment{}.TableName()), Constitutions: gormx.NewTableName(prefix, model.ProjectConstitution{}.TableName()), ReadinessChecks: gormx.NewTableName(prefix, model.ReadinessCheck{}.TableName()), Dependencies: gormx.NewTableName(prefix, model.WorkItemDependency{}.TableName()), WorkTransitions: gormx.NewTableName(prefix, model.WorkItemTransition{}.TableName()), WorkCriteria: gormx.NewTableName(prefix, model.WorkItemCriterion{}.TableName()), WorkContracts: gormx.NewTableName(prefix, model.WorkItemContract{}.TableName()), WorkItems: gormx.NewTableName(prefix, model.WorkItem{}.TableName()), Deliveries: gormx.NewTableName(prefix, model.Delivery{}.TableName()), Counters: gormx.NewTableName(prefix, model.ProjectCounter{}.TableName()), Requirements: gormx.NewTableName(prefix, model.Requirement{}.TableName()), Criteria: gormx.NewTableName(prefix, model.AcceptanceCriterion{}.TableName()), RequirementVersions: gormx.NewTableName(prefix, model.RequirementVersion{}.TableName()), Projects: gormx.NewTableName(prefix, model.Project{}.TableName()), Contacts: gormx.NewTableName(prefix, model.ProjectContact{}.TableName()), Repositories: gormx.NewTableName(prefix, model.ProjectRepository{}.TableName())}
}

func BindConstitutions(db *gorm.DB, names TableNames) *projectConstitution {
	return Use(db).ProjectConstitution.Table(names.Constitutions.String())
}
func BindProjects(db *gorm.DB, names TableNames) *project {
	return Use(db).Project.Table(names.Projects.String())
}
func BindContacts(db *gorm.DB, names TableNames) *projectContact {
	return Use(db).ProjectContact.Table(names.Contacts.String())
}
func BindRepositories(db *gorm.DB, names TableNames) *projectRepository {
	return Use(db).ProjectRepository.Table(names.Repositories.String())
}

func BindCounters(db *gorm.DB, names TableNames) *projectCounter {
	return Use(db).ProjectCounter.Table(names.Counters.String())
}
func BindRequirements(db *gorm.DB, names TableNames) *requirement {
	return Use(db).Requirement.Table(names.Requirements.String())
}
func BindCriteria(db *gorm.DB, names TableNames) *acceptanceCriterion {
	return Use(db).AcceptanceCriterion.Table(names.Criteria.String())
}
func BindRequirementVersions(db *gorm.DB, names TableNames) *requirementVersion {
	return Use(db).RequirementVersion.Table(names.RequirementVersions.String())
}

func BindWorkItems(db *gorm.DB, names TableNames) *workItem {
	return Use(db).WorkItem.Table(names.WorkItems.String())
}
func BindDeliveries(db *gorm.DB, names TableNames) *delivery {
	return Use(db).Delivery.Table(names.Deliveries.String())
}

func BindWorkCriteria(db *gorm.DB, names TableNames) *workItemCriterion {
	return Use(db).WorkItemCriterion.Table(names.WorkCriteria.String())
}
func BindWorkContracts(db *gorm.DB, names TableNames) *workItemContract {
	return Use(db).WorkItemContract.Table(names.WorkContracts.String())
}

func BindReadinessChecks(db *gorm.DB, names TableNames) *readinessCheck {
	return Use(db).ReadinessCheck.Table(names.ReadinessChecks.String())
}
func BindDependencies(db *gorm.DB, names TableNames) *workItemDependency {
	return Use(db).WorkItemDependency.Table(names.Dependencies.String())
}
func BindWorkTransitions(db *gorm.DB, names TableNames) *workItemTransition {
	return Use(db).WorkItemTransition.Table(names.WorkTransitions.String())
}

func BindAssignments(db *gorm.DB, names TableNames) *assignment {
	return Use(db).Assignment.Table(names.Assignments.String())
}

func BindStageTransitions(db *gorm.DB, names TableNames) *projectStageTransition {
	return Use(db).ProjectStageTransition.Table(names.StageTransitions.String())
}

func BindResultVersions(db *gorm.DB, names TableNames) *resultVersion {
	return Use(db).ResultVersion.Table(names.ResultVersions.String())
}

func BindResultEvidence(db *gorm.DB, names TableNames) *resultEvidence {
	return Use(db).ResultEvidence.Table(names.ResultEvidence.String())
}

func BindResultActions(db *gorm.DB, names TableNames) *resultAction {
	return Use(db).ResultAction.Table(names.ResultActions.String())
}

func BindResultHandoffs(db *gorm.DB, names TableNames) *resultHandoff {
	return Use(db).ResultHandoff.Table(names.ResultHandoffs.String())
}

func BindChanges(db *gorm.DB, names TableNames) *changeRecord {
	return Use(db).ChangeRecord.Table(names.Changes.String())
}

func BindChangeImpacts(db *gorm.DB, names TableNames) *changeImpact {
	return Use(db).ChangeImpact.Table(names.ChangeImpacts.String())
}

func BindResultFeedback(db *gorm.DB, names TableNames) *resultFeedback {
	return Use(db).ResultFeedback.Table(names.ResultFeedback.String())
}

func BindVerificationRuns(db *gorm.DB, names TableNames) *verificationRun {
	return Use(db).VerificationRun.Table(names.VerificationRuns.String())
}
func BindVerificationOverrides(db *gorm.DB, names TableNames) *verificationOverride {
	return Use(db).VerificationOverride.Table(names.VerificationOverrides.String())
}

func BindReviewPlans(db *gorm.DB, names TableNames) *reviewPlan {
	return Use(db).ReviewPlan.Table(names.ReviewPlans.String())
}

func BindReviewSheets(db *gorm.DB, n TableNames) *reviewSheet {
	return Use(db).ReviewSheet.Table(n.ReviewSheets.String())
}

func BindReviewOpinions(db *gorm.DB, n TableNames) *reviewOpinion {
	return Use(db).ReviewOpinion.Table(n.ReviewOpinions.String())
}

func BindReviewTypes(db *gorm.DB, n TableNames) *reviewType {
	return Use(db).ReviewType.Table(n.ReviewTypes.String())
}

func BindArtifactConsumptions(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Table(names.ArtifactConsumptions.String())
}

func BindWorkActivities(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Table(names.WorkActivities.String())
}

func BindCodeLinks(db *gorm.DB, names TableNames) *gorm.DB { return db.Table(names.CodeLinks.String()) }
func BindCodeLinkScans(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Table(names.CodeLinkScans.String())
}

func BindReportQualityReceipts(db *gorm.DB, n TableNames) *reportQualityReceipt {
	return Use(db).ReportQualityReceipt.Table(n.ReportQualityReceipts.String())
}
func BindReportQualityObservations(db *gorm.DB, n TableNames) *reportQualityObservation {
	return Use(db).ReportQualityObservation.Table(n.ReportQualityObservations.String())
}

func BindClarificationReceipts(db *gorm.DB, names TableNames) *clarificationReceipt {
	return Use(db).ClarificationReceipt.Table(names.ClarificationReceipts.String())
}

func BindModeChanges(db *gorm.DB, names TableNames) *projectModeChange {
	return Use(db).ProjectModeChange.Table(names.ModeChanges.String())
}

func BindAttentionSessions(db *gorm.DB, names TableNames) *attentionSession {
	return Use(db).AttentionSession.Table(names.AttentionSessions.String())
}

func BindBudgetEvents(db *gorm.DB, names TableNames) *projectBudgetEvent {
	return Use(db).ProjectBudgetEvent.Table(names.BudgetEvents.String())
}

func BindDeployments(db *gorm.DB, names TableNames) *deployment {
	return Use(db).Deployment.Table(names.Deployments.String())
}
