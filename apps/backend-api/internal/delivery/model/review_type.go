package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ReviewType stores team-wide configuration; sheets preserve their own snapshot.
type ReviewType struct {
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID  guid.ID   `gorm:"column:tenant_id;not null"`
	Document  string    `gorm:"column:document;not null"`
	Version   int       `gorm:"column:version;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (ReviewType) TableName() string { return "delivery_review_types" }
