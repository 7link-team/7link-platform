package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// IngressObservation is an immutable, source-attributed reference snapshot.
// It is separate from field synchronization outcomes and local adjudications.
type IngressObservation struct {
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID   guid.ID   `gorm:"column:project_id;not null"`
	MappingID   guid.ID   `gorm:"column:mapping_id;not null"`
	EventID     guid.ID   `gorm:"column:event_id;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	Source      string    `gorm:"column:source;size:16;not null"`
	Detail      string    `gorm:"column:detail;type:text;not null"`
	ContentHash string    `gorm:"column:content_hash;size:64;not null"`
}

func (IngressObservation) TableName() string { return "integration_ingress_observations" }
