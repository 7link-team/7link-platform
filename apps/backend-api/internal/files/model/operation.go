package model

import "7link.com/platform/internal/infra/guid"

// Operation holds the durable request identity, pinned plan and atomic result.
type Operation struct {
	ID                 guid.ID `gorm:"column:id;primaryKey;autoIncrement:false"`
	SpaceID            guid.ID `gorm:"column:space_id"`
	ActorID            guid.ID `gorm:"column:actor_id"`
	IdempotencyKey     string  `gorm:"column:idempotency_key"`
	RequestHash        string  `gorm:"column:request_hash"`
	RequestJSON        string  `gorm:"column:request_json"`
	RootsJSON          string  `gorm:"column:roots_json"`
	Action             string  `gorm:"column:action"`
	State              string  `gorm:"column:state"`
	Revision           int64   `gorm:"column:revision"`
	SpaceRevision      int64   `gorm:"column:space_revision"`
	DestinationID      guid.ID `gorm:"column:destination_id"`
	DestinationVersion int64   `gorm:"column:destination_version"`
	ItemCount          int     `gorm:"column:item_count"`
	NodeBudget         int     `gorm:"column:node_budget"`
	ClosureBudget      int     `gorm:"column:closure_budget"`
	ResultJSON         string  `gorm:"column:result_json"`
	ErrorCode          string  `gorm:"column:error_code"`
	CreatedAt          int64   `gorm:"column:created_at;autoCreateTime:false"`
	ExpiresAt          int64   `gorm:"column:expires_at"`
	CompletedAt        *int64  `gorm:"column:completed_at"`
}

func (Operation) TableName() string { return "file_operations" }

// OperationItem keeps a node snapshot and live preparation pins. Terminal
// operations release the nullable pins but retain their historical snapshot.
type OperationItem struct {
	ID              guid.ID  `gorm:"column:id;primaryKey;autoIncrement:false"`
	SpaceID         guid.ID  `gorm:"column:space_id"`
	OperationID     guid.ID  `gorm:"column:operation_id"`
	Ordinal         int      `gorm:"column:ordinal"`
	NodeID          guid.ID  `gorm:"column:node_id"`
	SnapshotJSON    string   `gorm:"column:snapshot_json"`
	PinnedNodeID    *guid.ID `gorm:"column:pinned_node_id"`
	PinnedVersionID *guid.ID `gorm:"column:pinned_version_id"`
	PinnedObjectID  *guid.ID `gorm:"column:pinned_object_id"`
}

func (OperationItem) TableName() string { return "file_operation_items" }
