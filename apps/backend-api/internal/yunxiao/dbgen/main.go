package main

import (
	"7link.com/platform/internal/yunxiao/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.IngressObservation{}, model.PlanningCarryover{}, model.Iteration{}, model.PlanningVersion{}, model.Milestone{}, model.WorkPlanning{}, model.AutomationRule{}, model.AutomationRun{}, model.IntakeChannel{}, model.Connection{}, model.ProjectMapping{}, model.ExternalRef{}, model.StateMapping{}, model.ImportJob{}, model.InboundEvent{}, model.SyncLog{}, model.WorklogRevision{}, model.Worklog{}, model.WorkflowConfig{})
}
