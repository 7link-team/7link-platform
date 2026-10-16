package main

import (
	"7link.com/platform/internal/collab/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() { gengorm.Generate(model.Channel{}, model.ChannelMember{}) }
