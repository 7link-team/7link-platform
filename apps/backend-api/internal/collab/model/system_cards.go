package model

import "7link.com/platform/internal/infra/guid"

// SystemCard metadata can only be written by trusted domain projectors.
type SystemCard struct {
	MessageID guid.ID `gorm:"column:message_id;primaryKey;autoIncrement:false"`
	TenantID  guid.ID `gorm:"column:tenant_id;not null"`
	ChannelID guid.ID `gorm:"column:channel_id;not null"`
	SourceID  guid.ID `gorm:"column:source_id;not null"`
	Kind      string  `gorm:"column:kind;not null"`
	Payload   string  `gorm:"column:payload;not null"`
	FoldKey   string  `gorm:"column:fold_key;not null"`
	Attention bool    `gorm:"column:attention;not null"`
}

func (SystemCard) TableName() string { return "collab_system_cards" }
