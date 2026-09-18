package model

import "7link.com/platform/internal/infra/guid"

// TrashBatch owns one atomic deletion, including all normalized selected roots.
type TrashBatch struct {
	ID         guid.ID  `gorm:"column:id;primaryKey;autoIncrement:false"`
	SpaceID    guid.ID  `gorm:"column:space_id"`
	DeletedBy  guid.ID  `gorm:"column:deleted_by"`
	DeletedAt  int64    `gorm:"column:deleted_at"`
	State      string   `gorm:"column:state"`
	NodeCount  int      `gorm:"column:node_count"`
	RootCount  int      `gorm:"column:root_count"`
	RestoredBy *guid.ID `gorm:"column:restored_by"`
	RestoredAt *int64   `gorm:"column:restored_at"`
}

func (TrashBatch) TableName() string { return "file_trash_batches" }

// TrashRoot preserves the original placement without materializing a full path.
type TrashRoot struct {
	SpaceID    guid.ID `gorm:"column:space_id;primaryKey;autoIncrement:false"`
	BatchID    guid.ID `gorm:"column:batch_id;primaryKey;autoIncrement:false"`
	NodeID     guid.ID `gorm:"column:node_id;primaryKey;autoIncrement:false"`
	ParentID   guid.ID `gorm:"column:parent_id"`
	ParentKind string  `gorm:"column:parent_kind"`
	Name       string  `gorm:"column:name"`
	NameKey    string  `gorm:"column:name_key"`
	Ext        string  `gorm:"column:ext"`
}

func (TrashRoot) TableName() string { return "file_trash_roots" }
