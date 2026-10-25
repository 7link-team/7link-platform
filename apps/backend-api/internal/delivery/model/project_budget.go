package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ProjectBudgetEvent is an immutable observation, never an estimated spend.
type ProjectBudgetEvent struct {
	ThresholdLock   *string `gorm:"column:threshold_lock" json:"-"`
	ThresholdPct    *int    `gorm:"column:threshold_pct" json:"threshold_pct"`
	CapBeforeMicros *int64  `gorm:"column:cap_before_micros" json:"cap_before_micros"`
	AuthoritySource string  `gorm:"column:authority_source;not null" json:"authority_source"`

	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false" json:"tenant_id"`
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	ProjectID   guid.ID   `gorm:"column:project_id;not null" json:"project_id"`
	Kind        string    `gorm:"column:kind;not null" json:"kind"`
	CapMicros   *int64    `gorm:"column:cap_micros" json:"cap_micros"`
	SpentMicros int64     `gorm:"column:spent_micros;not null" json:"spent_micros"`
	NotifyPct   int       `gorm:"column:notify_pct;not null" json:"notify_pct"`
	CapVersion  int       `gorm:"column:cap_version;not null" json:"cap_version"`
	State       string    `gorm:"column:state;not null" json:"state"`
	Reason      string    `gorm:"column:reason;not null" json:"reason"`
	ActorID     *guid.ID  `gorm:"column:actor_id" json:"actor_id"`
	OccurredAt  time.Time `gorm:"column:occurred_at;not null;autoCreateTime:false" json:"occurred_at"`
}

func (ProjectBudgetEvent) TableName() string { return "delivery_project_budget_events" }
