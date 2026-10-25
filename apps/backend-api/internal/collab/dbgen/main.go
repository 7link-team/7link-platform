package main

import (
	"7link.com/platform/internal/collab/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.SystemCard{}, model.Channel{}, model.ChannelMember{}, model.Message{}, model.MessageMention{}, model.SavedMessage{}, model.MessageAttachment{}, model.MessageWork{}, model.MessageRevision{})
}
