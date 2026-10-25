package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Tolerance serializes commitments and their unresolved exception.
type Tolerance struct {
	ForecastMeasurement   *string    `gorm:"column:forecast_measurement"`
	TenantID              guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID                    guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	ProjectID             guid.ID    `gorm:"column:project_id;not null"`
	SubjectKind           string     `gorm:"column:subject_kind;not null"`
	SubjectID             guid.ID    `gorm:"column:subject_id;not null"`
	AppetiteEndAt         time.Time  `gorm:"column:appetite_end_at;not null"`
	AppetiteCostMicros    *int64     `gorm:"column:appetite_cost_micros"`
	AppetiteScopeCriteria *int64     `gorm:"column:appetite_scope_criteria"`
	ToleranceTimePct      int        `gorm:"column:tolerance_time_pct;not null"`
	ToleranceCostPct      int        `gorm:"column:tolerance_cost_pct;not null"`
	ToleranceScopePct     int        `gorm:"column:tolerance_scope_pct;not null"`
	ForecastEndAt         *time.Time `gorm:"column:forecast_end_at"`
	ForecastCostMicros    *int64     `gorm:"column:forecast_cost_micros"`
	ForecastScopeCriteria *int64     `gorm:"column:forecast_scope_criteria"`
	ForecastAt            *time.Time `gorm:"column:forecast_at"`
	State                 string     `gorm:"column:state;not null"`
	OpenExceptionID       *guid.ID   `gorm:"column:open_exception_id"`
	BaselineNo            int        `gorm:"column:baseline_no;not null"`
	Version               int        `gorm:"column:version;not null"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (Tolerance) TableName() string { return "clientwork_tolerances" }

type ToleranceException struct {
	TenantID           guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID                 guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	ProjectID          guid.ID    `gorm:"column:project_id;not null"`
	ToleranceID        guid.ID    `gorm:"column:tolerance_id;not null"`
	BreachedDimensions string     `gorm:"column:breached_dimensions;not null"`
	ForecastSnapshot   string     `gorm:"column:forecast_snapshot;not null"`
	Report             *string    `gorm:"column:report"`
	State              string     `gorm:"column:state;not null"`
	Decision           *string    `gorm:"column:decision"`
	DecisionPayload    *string    `gorm:"column:decision_payload"`
	DecidedBy          *guid.ID   `gorm:"column:decided_by"`
	DecidedAt          *time.Time `gorm:"column:decided_at"`
	DecisionReason     *string    `gorm:"column:decision_reason"`
	Version            int        `gorm:"column:version;not null"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (ToleranceException) TableName() string { return "clientwork_exceptions" }
