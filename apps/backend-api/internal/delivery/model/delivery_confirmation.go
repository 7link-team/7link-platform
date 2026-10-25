package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// DeliveryConfirmation binds a request to one actual client package revision.
// Published, edited or archived sources cease to be pending without rewriting history.
type DeliveryConfirmation struct {
	ID            guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID      guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID     guid.ID    `gorm:"column:project_id;not null"`
	PackageID     guid.ID    `gorm:"column:package_id;not null"`
	SourceVersion int        `gorm:"column:source_version;not null"`
	ActiveKey     *string    `gorm:"column:active_key"`
	RequestedBy   guid.ID    `gorm:"column:requested_by;not null"`
	RequestedAt   time.Time  `gorm:"column:requested_at;not null;autoCreateTime:false"`
	WithdrawnAt   *time.Time `gorm:"column:withdrawn_at"`
	WithdrawnBy   *guid.ID   `gorm:"column:withdrawn_by"`
	Version       int        `gorm:"column:version;not null"`
}

func (DeliveryConfirmation) TableName() string { return "delivery_confirmation_requests" }
