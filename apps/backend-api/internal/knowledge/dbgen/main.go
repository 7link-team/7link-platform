package main

import (
	"7link.com/platform/internal/knowledge/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.Entry{}, model.Review{}, model.Settings{}, model.Source{}, model.Chunk{}, model.UsageReceipt{}, model.Skill{}, model.SkillVersion{}, model.AgentSkill{}, model.DiagnosticLog{}, model.RuntimeMetricSample{}, model.InjectionLog{}, model.SearchLog{}, model.EmbeddingConfig{}, model.Distillation{})
}
