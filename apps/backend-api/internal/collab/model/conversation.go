package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Message struct {
	ID               guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID    `gorm:"column:tenant_id;not null"`
	ChannelID        guid.ID    `gorm:"column:channel_id;not null"`
	Seq              int64      `gorm:"column:seq;not null"`
	ContextSeq       int64      `gorm:"column:context_seq;not null;default:0"`
	AuthorID         guid.ID    `gorm:"column:author_id;not null"`
	Body             string     `gorm:"column:body;not null"`
	ReplyToMessageID *guid.ID   `gorm:"column:reply_to_message_id;"`
	Visibility       string     `gorm:"column:visibility;not null"`
	WithdrawnAt      *time.Time `gorm:"column:withdrawn_at"`
	Version          int        `gorm:"column:version;not null"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt        time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Message) TableName() string { return "collab_messages" }

type MessageMention struct {
	MessageID   guid.ID `gorm:"column:message_id;primaryKey;autoIncrement:false"`
	PrincipalID guid.ID `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	TenantID    guid.ID `gorm:"column:tenant_id;not null"`
	ChannelID   guid.ID `gorm:"column:channel_id;not null"`
}

func (MessageMention) TableName() string { return "collab_message_mentions" }

type SavedMessage struct {
	MessageID   guid.ID   `gorm:"column:message_id;primaryKey;autoIncrement:false"`
	PrincipalID guid.ID   `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	ChannelID   guid.ID   `gorm:"column:channel_id;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (SavedMessage) TableName() string { return "collab_saved_messages" }

type MessageAttachment struct {
	ID        guid.ID `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID  guid.ID `gorm:"column:tenant_id;not null"`
	ChannelID guid.ID `gorm:"column:channel_id;not null"`
	MessageID guid.ID `gorm:"column:message_id;not null"`
	FileID    guid.ID `gorm:"column:file_id;not null"`
	VersionID guid.ID `gorm:"column:version_id;not null"`
}

func (MessageAttachment) TableName() string { return "collab_message_attachments" }

// MessageWork preserves source association atomically with delivery creation.
type MessageWork struct {
	TenantID   guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	MessageID  guid.ID   `gorm:"column:message_id;primaryKey;autoIncrement:false;not null"`
	ChannelID  guid.ID   `gorm:"column:channel_id;not null"`
	ProjectID  guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID guid.ID   `gorm:"column:work_item_id;not null"`
	CreatedBy  guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (MessageWork) TableName() string { return "collab_message_work_items" }

// MessageRevision preserves the prior text for audit-authorized readers only.
type MessageRevision struct {
	MessageID guid.ID   `gorm:"column:message_id;primaryKey;autoIncrement:false" json:"message_id"`
	Revision  int       `gorm:"column:revision;primaryKey;autoIncrement:false" json:"revision"`
	TenantID  guid.ID   `gorm:"column:tenant_id;not null" json:"-"`
	Body      string    `gorm:"column:body;not null" json:"body"`
	EditedBy  guid.ID   `gorm:"column:edited_by;not null" json:"edited_by"`
	EditedAt  time.Time `gorm:"column:edited_at;not null;autoCreateTime:false" json:"edited_at"`
}

func (MessageRevision) TableName() string { return "collab_message_revisions" }
