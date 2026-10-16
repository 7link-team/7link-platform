package query

import (
	"7link.com/platform/internal/collab/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct{ Channels, Members gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{Channels: gormx.NewTableName(prefix, model.Channel{}.TableName()), Members: gormx.NewTableName(prefix, model.ChannelMember{}.TableName())}
}
func BindChannels(db *gorm.DB, names TableNames) *channel {
	return Use(db).Channel.Table(names.Channels.String())
}
func BindMembers(db *gorm.DB, names TableNames) *channelMember {
	return Use(db).ChannelMember.Table(names.Members.String())
}
