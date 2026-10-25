package main

import (
	"7link.com/platform/internal/governance/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.InboxAcknowledgement{}, model.ProjectEscalationRule{}, model.SpawnRequest{}, model.AutomatedDecision{}, model.QualityEscape{}, model.RoleSeed{}, model.Settings{}, model.EscalationRule{}, model.EscalationIncident{}, model.WorkflowDecision{}, model.Delegation{}, model.ClaimEvaluation{}, model.SpawnGrant{}, model.SpawnGrantMember{}, model.SpawnCostCursor{})
}
