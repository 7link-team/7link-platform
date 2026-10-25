package query

import (
	"7link.com/platform/internal/collab/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct{ Channels, Members, Messages, Mentions, Saved, Attachments, MessageWorks, Revisions, Connectors, Deliveries, SystemCards gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{SystemCards: gormx.NewTableName(prefix, model.SystemCard{}.TableName()), Connectors: gormx.NewTableName(prefix, model.ChannelConnector{}.TableName()), Deliveries: gormx.NewTableName(prefix, model.ChannelDelivery{}.TableName()), Revisions: gormx.NewTableName(prefix, model.MessageRevision{}.TableName()), MessageWorks: gormx.NewTableName(prefix, model.MessageWork{}.TableName()), Messages: gormx.NewTableName(prefix, model.Message{}.TableName()), Mentions: gormx.NewTableName(prefix, model.MessageMention{}.TableName()), Saved: gormx.NewTableName(prefix, model.SavedMessage{}.TableName()), Attachments: gormx.NewTableName(prefix, model.MessageAttachment{}.TableName()), Channels: gormx.NewTableName(prefix, model.Channel{}.TableName()), Members: gormx.NewTableName(prefix, model.ChannelMember{}.TableName())}
}
func BindChannels(db *gorm.DB, names TableNames) *channel {
	return Use(db).Channel.Table(names.Channels.String())
}
func BindMembers(db *gorm.DB, names TableNames) *channelMember {
	return Use(db).ChannelMember.Table(names.Members.String())
}

func BindMessages(db *gorm.DB, names TableNames) *message {
	return Use(db).Message.Table(names.Messages.String())
}
func BindMentions(db *gorm.DB, names TableNames) *messageMention {
	return Use(db).MessageMention.Table(names.Mentions.String())
}
func BindSaved(db *gorm.DB, names TableNames) *savedMessage {
	return Use(db).SavedMessage.Table(names.Saved.String())
}
func BindAttachments(db *gorm.DB, names TableNames) *messageAttachment {
	return Use(db).MessageAttachment.Table(names.Attachments.String())
}

func BindMessageWorks(db *gorm.DB, names TableNames) *messageWork {
	return Use(db).MessageWork.Table(names.MessageWorks.String())
}

func BindRevisions(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Table(names.Revisions.String())
}

func BindConnectors(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Table(names.Connectors.String())
}
func BindDeliveries(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Table(names.Deliveries.String())
}

func BindSystemCards(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Table(names.SystemCards.String())
}

func BindSystemCardRecords(db *gorm.DB, names TableNames) *systemCard {
	return Use(db).SystemCard.Table(names.SystemCards.String())
}
