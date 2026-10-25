package main

import (
	"7link.com/platform/internal/planning/model"
	g "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	g.Generate(model.GoalSnapshot{}, model.HeavyRun{}, model.Goal{}, model.Plan{}, model.Report{}, model.ReportSubmission{}, model.ReportCheck{}, model.Settings{}, model.LoopContract{}, model.LoopRun{}, model.Contributor{})
}
