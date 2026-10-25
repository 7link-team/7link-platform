package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// LifecycleReceipt is an immutable observation supplied by a trusted domain
// capability, never by descriptive Studio text or an HTTP verdict field.
type LifecycleReceipt struct {
	ID                guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	TenantID          guid.ID   `gorm:"column:tenant_id;not null" json:"-"`
	ProjectID         guid.ID   `gorm:"column:project_id;not null" json:"project_id"`
	TemplateVersionID guid.ID   `gorm:"column:template_version_id;not null" json:"template_version_id"`
	ObjectKey         string    `gorm:"column:object_key;size:64;not null" json:"object_key"`
	SourceKey         string    `gorm:"column:source_key;size:128;not null" json:"source_key"`
	EventType         string    `gorm:"column:event_type;size:64;not null" json:"event_type"`
	State             string    `gorm:"column:state;size:24;not null" json:"state"`
	Payload           string    `gorm:"column:payload;not null" json:"-"`
	RequestHash       string    `gorm:"column:request_hash;size:64;not null" json:"-"`
	CreatedBy         guid.ID   `gorm:"column:created_by;not null" json:"created_by"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
}

func (LifecycleReceipt) TableName() string { return "templates_lifecycle_receipts" }
