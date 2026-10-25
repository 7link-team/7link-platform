package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// HeavyRun stores planned per-branch caps separately from actual reservations.
// Only Execution's canonical launch can reserve or report runtime progress.
type HeavyRun struct {
	ID         guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	TenantID   guid.ID   `gorm:"column:tenant_id;not null" json:"tenant_id"`
	ProjectID  guid.ID   `gorm:"column:project_id;not null" json:"project_id"`
	WorkItemID guid.ID   `gorm:"column:work_item_id;not null" json:"work_item_id"`
	State      string    `gorm:"column:state;not null" json:"state"`
	Reason     string    `gorm:"column:reason;not null" json:"reason"`
	Branches   string    `gorm:"column:branches;not null" json:"-"`
	CreatedBy  guid.ID   `gorm:"column:created_by;not null" json:"created_by"`
	Version    int       `gorm:"column:version;not null" json:"version"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
}

func (HeavyRun) TableName() string { return "planning_heavy_runs" }
