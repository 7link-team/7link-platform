package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// SpawnGrant stores the Human-approved scope and serialized quota counters.
// ActiveKey is non-null for ACTIVE/EXHAUSTED and unique within a tenant.
type SpawnGrant struct {
	ID                 guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID           guid.ID    `gorm:"column:tenant_id;not null"`
	GranteeAgentID     guid.ID    `gorm:"column:grantee_agent_id;not null"`
	GranteePrincipalID guid.ID    `gorm:"column:grantee_principal_id;not null"`
	GrantedBy          guid.ID    `gorm:"column:granted_by;not null"`
	ScopeJSON          string     `gorm:"column:scope_json;not null"`
	Reason             string     `gorm:"column:reason;not null"`
	StartsAt           time.Time  `gorm:"column:starts_at;not null"`
	EndsAt             time.Time  `gorm:"column:ends_at;not null"`
	State              string     `gorm:"column:state;not null"`
	ActiveKey          *string    `gorm:"column:active_key;size:32"`
	EndReason          *string    `gorm:"column:end_reason"`
	EndedAt            *time.Time `gorm:"column:ended_at"`
	EndedBy            *guid.ID   `gorm:"column:ended_by"`
	EndNote            *string    `gorm:"column:end_note"`
	ActiveCount        int        `gorm:"column:active_count;not null"`
	SpawnedDay         string     `gorm:"column:spawned_day;size:10;not null"`
	SpawnedToday       int        `gorm:"column:spawned_today;not null"`
	SpentMicros        int64      `gorm:"column:spent_micros;not null"`
	ReplacesGrantID    *guid.ID   `gorm:"column:replaces_grant_id"`
	Version            int        `gorm:"column:version;not null"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (SpawnGrant) TableName() string { return "governance_spawn_grants" }

// SpawnGrantMember is the idempotent reservation and per-Agent cost snapshot.
type SpawnGrantMember struct {
	ID                guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID          guid.ID    `gorm:"column:tenant_id;not null"`
	GrantID           guid.ID    `gorm:"column:grant_id;not null"`
	AgentID           guid.ID    `gorm:"column:agent_id;not null"`
	State             string     `gorm:"column:state;not null"`
	ReleaseReason     *string    `gorm:"column:release_reason"`
	PerAgentCapMicros int64      `gorm:"column:per_agent_cap_micros;not null"`
	SpentMicros       int64      `gorm:"column:spent_micros;not null"`
	CapNotified       bool       `gorm:"column:cap_notified;not null"`
	ReservedAt        time.Time  `gorm:"column:reserved_at;not null;autoCreateTime:false"`
	ReleasedAt        *time.Time `gorm:"column:released_at"`
}

func (SpawnGrantMember) TableName() string { return "governance_spawn_grant_members" }

// SpawnCostCursor prevents duplicate and out-of-order cumulative cost delivery
// from charging a grant twice. It participates in the consumer transaction.
type SpawnCostCursor struct {
	ID                   guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID             guid.ID   `gorm:"column:tenant_id;not null"`
	ExecutionID          guid.ID   `gorm:"column:execution_id;not null"`
	AgentID              guid.ID   `gorm:"column:agent_id;not null"`
	LastCumulativeMicros int64     `gorm:"column:last_cumulative_micros;not null"`
	UpdatedAt            time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (SpawnCostCursor) TableName() string { return "governance_spawn_cost_cursors" }
