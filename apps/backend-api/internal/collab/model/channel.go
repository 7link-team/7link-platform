// Package model contains collaboration persistence models.
package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Channel struct {
	ID            guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID      guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID     *guid.ID   `gorm:"column:project_id"`
	Kind          string     `gorm:"column:kind;not null"`
	Name          string     `gorm:"column:name;size:40;not null"`
	Topic         *string    `gorm:"column:topic"`
	DirectPairKey *string    `gorm:"column:direct_pair_key"`
	LastSeq       int64      `gorm:"column:last_seq;not null"`
	LastMessageAt *time.Time `gorm:"column:last_message_at"`
	ArchivedAt    *time.Time `gorm:"column:archived_at"`
	Version       int        `gorm:"column:version;not null"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	CreatedBy     *guid.ID   `gorm:"column:created_by"`
	UpdatedBy     *guid.ID   `gorm:"column:updated_by"`
}

func (Channel) TableName() string { return "collab_channels" }

type ChannelMember struct {
	ChannelID   guid.ID    `gorm:"column:channel_id;primaryKey;autoIncrement:false"`
	PrincipalID guid.ID    `gorm:"column:principal_id;primaryKey;autoIncrement:false"`
	TenantID    guid.ID    `gorm:"column:tenant_id;not null"`
	MemberKind  string     `gorm:"column:member_kind;not null"`
	Role        string     `gorm:"column:role;not null"`
	Source      string     `gorm:"column:source;not null"`
	LastReadSeq int64      `gorm:"column:last_read_seq;not null"`
	Muted       bool       `gorm:"column:muted;not null"`
	JoinedAt    time.Time  `gorm:"column:joined_at;not null"`
	LeftAt      *time.Time `gorm:"column:left_at"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	CreatedBy   *guid.ID   `gorm:"column:created_by"`
	UpdatedBy   *guid.ID   `gorm:"column:updated_by"`
}

func (ChannelMember) TableName() string { return "collab_channel_members" }
