package main

import (
	"7link.com/platform/internal/delivery/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.Deployment{}, model.ProjectBudgetEvent{}, model.AttentionSession{}, model.DeliveryConfirmation{}, model.ProjectModeChange{}, model.ClarificationReceipt{}, model.CommentDefect{}, model.ReportQualityReceipt{}, model.ReportQualityObservation{}, model.CodeLink{}, model.CodeLinkScan{}, model.WorkItemActivity{}, model.ArtifactConsumption{}, model.ReviewType{}, model.ReviewOpinion{}, model.ReviewSheet{}, model.ReviewPlan{}, model.Project{}, model.ProjectContact{}, model.ProjectRepository{}, model.ProjectCounter{}, model.Requirement{}, model.AcceptanceCriterion{}, model.RequirementVersion{}, model.WorkItem{}, model.Delivery{}, model.WorkItemCriterion{}, model.WorkItemContract{}, model.ReadinessCheck{}, model.WorkItemDependency{}, model.WorkItemTransition{}, model.ProjectConstitution{}, model.Assignment{}, model.ProjectStageTransition{}, model.ResultVersion{}, model.ResultEvidence{}, model.ResultAction{}, model.ResultHandoff{}, model.ChangeRecord{}, model.ChangeImpact{}, model.ResultFeedback{}, model.VerificationRun{}, model.VerificationOverride{})
}
