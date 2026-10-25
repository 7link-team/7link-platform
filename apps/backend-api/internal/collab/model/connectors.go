package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ChannelConnector stores an authorized channel's outbound HTTP destination.
type ChannelConnector struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null"`
	ChannelID    guid.ID   `gorm:"column:channel_id;not null"`
	CreatedBy    guid.ID   `gorm:"column:created_by;not null"`
	Kind         string    `gorm:"column:kind;not null"`
	Endpoint     string    `gorm:"column:endpoint;not null"`
	SecretSealed string    `gorm:"column:secret_sealed;not null"`
	Enabled      bool      `gorm:"column:enabled;not null"`
	Version      int       `gorm:"column:version;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (ChannelConnector) TableName() string { return "collab_channel_connectors" }

// ChannelDelivery references current message content instead of retaining it.
type ChannelDelivery struct {
	ID            guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID      guid.ID   `gorm:"column:tenant_id;not null"`
	ChannelID     guid.ID   `gorm:"column:channel_id;not null"`
	ConnectorID   guid.ID   `gorm:"column:connector_id;not null"`
	MessageID     guid.ID   `gorm:"column:message_id;not null"`
	ConfigVersion int       `gorm:"column:config_version;not null"`
	State         string    `gorm:"column:state;not null"`
	Attempts      int       `gorm:"column:attempts;not null"`
	NextAt        time.Time `gorm:"column:next_at;not null"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (ChannelDelivery) TableName() string { return "collab_channel_deliveries" }
