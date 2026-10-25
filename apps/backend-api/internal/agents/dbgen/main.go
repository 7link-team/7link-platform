package main

import (
	"7link.com/platform/internal/agents/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.Retirement{}, model.ProcessEvent{}, model.LiveState{}, model.InboxEvent{}, model.Reset{}, model.Reminder{}, model.ToolEntry{}, model.ToolGrant{}, model.ToolVersion{}, model.ToolUpstream{}, model.ManifestVersion{})
}
