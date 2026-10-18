package main

import (
	"7link.com/platform/internal/delivery/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.Project{}, model.ProjectContact{}, model.ProjectRepository{}, model.ProjectCounter{}, model.Requirement{}, model.AcceptanceCriterion{}, model.RequirementVersion{}, model.WorkItem{}, model.Delivery{})
}
