package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Cursor persists the sequencer high-water mark independently of event retention.
type Cursor struct {
	ID                   guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	Consumer             string    `gorm:"column:consumer;size:128;not null"`
	PartitionNo          int       `gorm:"column:partition_no;not null"`
	LastSeq              int64     `gorm:"column:last_seq;not null"`
	FenceToken           int64     `gorm:"column:fence_token;not null"`
	Paused               bool      `gorm:"column:paused;not null"`
	ReplayOf             *string   `gorm:"column:replay_of;size:96"`
	ReplayToSeq          *int64    `gorm:"column:replay_to_seq"`
	ReplayIgnoreReceipts bool      `gorm:"column:replay_ignore_receipts;not null"`
	UpdatedAt            time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Cursor) TableName() string { return "domain_event_cursors" }
