package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// StudioRecord belongs to one immutable template binding. Reports are human
// supplied domain notes, never claims about platform execution or verification.
type StudioRecord struct {
	ID         guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	TenantID   guid.ID   `gorm:"column:tenant_id;not null" json:"-"`
	BindingID  guid.ID   `gorm:"column:binding_id;not null" json:"binding_id"`
	Domain     string    `gorm:"column:domain;size:16;not null" json:"domain"`
	Kind       string    `gorm:"column:kind;size:32;not null" json:"kind"`
	ParentID   *guid.ID  `gorm:"column:parent_id" json:"parent_id,omitempty"`
	SelectedID *guid.ID  `gorm:"column:selected_id" json:"selected_id,omitempty"`
	State      string    `gorm:"column:state;size:16;not null" json:"state"`
	Payload    string    `gorm:"column:payload;not null" json:"-"`
	Version    int       `gorm:"column:version;not null" json:"version"`
	CreatedBy  guid.ID   `gorm:"column:created_by;not null" json:"created_by"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
}

func (StudioRecord) TableName() string { return "templates_studio_records" }
