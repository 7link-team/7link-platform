package model

import "7link.com/platform/internal/infra/guid"

// Space is a tenant or account namespace, not an implicit access grant.
type Space struct {
	ID          guid.ID  `gorm:"column:id;primaryKey;autoIncrement:false"`
	ScopeKind   string   `gorm:"column:scope_kind"`
	TenantID    *guid.ID `gorm:"column:tenant_id"`
	PrincipalID *guid.ID `gorm:"column:principal_id"`
	Version     int64    `gorm:"column:version"`
	CreatedAt   int64    `gorm:"column:created_at;autoCreateTime:false"`
}

func (Space) TableName() string { return "file_spaces" }

// Node stores a virtual name and parent, never a materialized storage path.
type Node struct {
	ID               guid.ID  `gorm:"column:id;primaryKey;autoIncrement:false"`
	SpaceID          guid.ID  `gorm:"column:space_id"`
	ParentID         *guid.ID `gorm:"column:parent_id"`
	ParentKind       string   `gorm:"column:parent_kind"`
	RootSlot         *int     `gorm:"column:root_slot"`
	LiveSlot         *int     `gorm:"column:live_slot"`
	Kind             string   `gorm:"column:kind"`
	Name             string   `gorm:"column:name"`
	NameKey          string   `gorm:"column:name_key"`
	Ext              string   `gorm:"column:ext"`
	State            string   `gorm:"column:state"`
	Version          int64    `gorm:"column:version"`
	CurrentVersionID *guid.ID `gorm:"column:current_version_id"`
	TrashBatchID     *guid.ID `gorm:"column:trash_batch_id"`
	CreatedAt        int64    `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt        int64    `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (Node) TableName() string { return "file_nodes" }

// NodeClosure contains all ancestor relations, including depth-zero self rows.
type NodeClosure struct {
	SpaceID      guid.ID `gorm:"column:space_id;primaryKey;autoIncrement:false"`
	AncestorID   guid.ID `gorm:"column:ancestor_id;primaryKey;autoIncrement:false"`
	DescendantID guid.ID `gorm:"column:descendant_id;primaryKey;autoIncrement:false"`
	Depth        int     `gorm:"column:depth"`
}

func (NodeClosure) TableName() string { return "file_node_closure" }
