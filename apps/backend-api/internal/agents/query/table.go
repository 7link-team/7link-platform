package query

import (
	"7link.com/platform/internal/agents/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct{ Retirement, ProcessEvent, LiveState, InboxEvent, Reset, Reminder, ToolEntry, ToolGrant, ToolVersion, ToolUpstream, ManifestVersion gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{Retirement: gormx.NewTableName(prefix, model.Retirement{}.TableName()), ProcessEvent: gormx.NewTableName(prefix, model.ProcessEvent{}.TableName()), ManifestVersion: gormx.NewTableName(prefix, model.ManifestVersion{}.TableName()), LiveState: gormx.NewTableName(prefix, model.LiveState{}.TableName()), InboxEvent: gormx.NewTableName(prefix, model.InboxEvent{}.TableName()), Reset: gormx.NewTableName(prefix, model.Reset{}.TableName()), Reminder: gormx.NewTableName(prefix, model.Reminder{}.TableName()), ToolEntry: gormx.NewTableName(prefix, model.ToolEntry{}.TableName()), ToolGrant: gormx.NewTableName(prefix, model.ToolGrant{}.TableName()), ToolVersion: gormx.NewTableName(prefix, model.ToolVersion{}.TableName()), ToolUpstream: gormx.NewTableName(prefix, model.ToolUpstream{}.TableName())}
}

func BindLiveState(db *gorm.DB, names TableNames) *liveState {
	return Use(db).LiveState.Table(names.LiveState.String())
}
func BindInboxEvent(db *gorm.DB, names TableNames) *inboxEvent {
	return Use(db).InboxEvent.Table(names.InboxEvent.String())
}
func BindReset(db *gorm.DB, names TableNames) *reset {
	return Use(db).Reset.Table(names.Reset.String())
}
func BindReminder(db *gorm.DB, names TableNames) *reminder {
	return Use(db).Reminder.Table(names.Reminder.String())
}
func BindToolEntry(db *gorm.DB, names TableNames) *toolEntry {
	return Use(db).ToolEntry.Table(names.ToolEntry.String())
}
func BindToolGrant(db *gorm.DB, names TableNames) *toolGrant {
	return Use(db).ToolGrant.Table(names.ToolGrant.String())
}

func BindManifestVersion(db *gorm.DB, names TableNames) *manifestVersion {
	return Use(db).ManifestVersion.Table(names.ManifestVersion.String())
}

func BindToolVersion(db *gorm.DB, names TableNames) *toolVersion {
	return Use(db).ToolVersion.Table(names.ToolVersion.String())
}
func BindToolUpstream(db *gorm.DB, names TableNames) *toolUpstream {
	return Use(db).ToolUpstream.Table(names.ToolUpstream.String())
}

func BindProcessEvents(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Table(names.ProcessEvent.String()).Model(&model.ProcessEvent{})
}

func BindRetirement(db *gorm.DB, names TableNames) *retirement {
	return Use(db).Retirement.Table(names.Retirement.String())
}
