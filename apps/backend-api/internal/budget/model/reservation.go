package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Reservation retains the original estimate and final cost for exact replay.
type Reservation struct {
	ID             guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID       guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID      guid.ID    `gorm:"column:project_id;not null"`
	Source         string     `gorm:"column:source;size:16;not null"`
	SourceID       guid.ID    `gorm:"column:source_id;not null"`
	ReservedMicros int64      `gorm:"column:reserved_micros;not null"`
	PriceKnown     bool       `gorm:"column:price_known;not null"`
	ActualMicros   *int64     `gorm:"column:actual_micros"`
	State          string     `gorm:"column:state;size:16;not null"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	SettledAt      *time.Time `gorm:"column:settled_at"`
}

func (Reservation) TableName() string { return "budget_reservations" }
