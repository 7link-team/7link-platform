package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ProjectModeChange records a mode or takeover control receipt and its guards.
// A TAKEOVER may retain the same mode while revoking current Agent authority.
type ProjectModeChange struct {
	TenantID              guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false" json:"-"`
	ID                    guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	ProjectID             guid.ID   `gorm:"column:project_id;not null" json:"project_id"`
	FromMode              string    `gorm:"column:from_mode;not null" json:"from_mode"`
	ToMode                string    `gorm:"column:to_mode;not null" json:"to_mode"`
	Trigger               string    `gorm:"column:trigger_kind;not null" json:"trigger"`
	Reason                string    `gorm:"column:reason;not null" json:"reason"`
	PreconditionsSnapshot string    `gorm:"column:preconditions_snapshot;not null" json:"-"`
	RevokedDelegationIDs  string    `gorm:"column:revoked_delegation_ids;not null" json:"-"`
	RevokedSpawnGrantIDs  string    `gorm:"column:revoked_spawn_grant_ids;not null" json:"-"`
	ActorID               guid.ID   `gorm:"column:actor_id;not null" json:"actor_id"`
	AuthoritySource       string    `gorm:"column:authority_source;not null" json:"authority_source"`
	VersionAfter          int       `gorm:"column:version_after;not null" json:"version_after"`
	OccurredAt            time.Time `gorm:"column:occurred_at;not null;autoCreateTime:false" json:"occurred_at"`
}

func (ProjectModeChange) TableName() string { return "delivery_project_mode_changes" }
