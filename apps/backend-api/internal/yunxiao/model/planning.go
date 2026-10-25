package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type PlanningBase struct {
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID  guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID guid.ID   `gorm:"column:project_id;not null"`
	Name      string    `gorm:"column:name;size:120;not null"`
	Version   int       `gorm:"column:version;not null"`
	CreatedBy guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}
type Iteration struct {
	PlanningBase
	Goal     string     `gorm:"column:goal;size:500;not null"`
	GoalID   *guid.ID   `gorm:"column:goal_id"`
	StartsAt time.Time  `gorm:"column:starts_at;not null"`
	EndsAt   time.Time  `gorm:"column:ends_at;not null"`
	State    string     `gorm:"column:state;size:16;not null"`
	ClosedAt *time.Time `gorm:"column:closed_at"`
}

func (Iteration) TableName() string { return "delivery_iterations" }

type PlanningVersion struct {
	PlanningBase
	PlannedReleaseAt *time.Time `gorm:"column:planned_release_at"`
	ReleasedAt       *time.Time `gorm:"column:released_at"`
	State            string     `gorm:"column:state;size:16;not null"`
	LockedAt         *time.Time `gorm:"column:locked_at"`
	LockedBy         *guid.ID   `gorm:"column:locked_by"`
	DeliveryID       *guid.ID   `gorm:"column:delivery_id"`
}

func (PlanningVersion) TableName() string { return "delivery_versions" }

type Milestone struct {
	PlanningBase
	DueAt      time.Time  `gorm:"column:due_at;not null"`
	AchievedAt *time.Time `gorm:"column:achieved_at"`
	GoalID     *guid.ID   `gorm:"column:goal_id"`
}

func (Milestone) TableName() string { return "delivery_milestones" }

// WorkPlanning owns planning membership without changing delivery-owned rows.
type WorkPlanning struct {
	ID            guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID      guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID     guid.ID    `gorm:"column:project_id;not null"`
	WorkItemID    guid.ID    `gorm:"column:work_item_id;not null"`
	IterationID   *guid.ID   `gorm:"column:iteration_id"`
	VersionID     *guid.ID   `gorm:"column:version_id"`
	MilestoneID   *guid.ID   `gorm:"column:milestone_id"`
	CarriedFromID *guid.ID   `gorm:"column:carried_from_id"`
	CarriedAt     *time.Time `gorm:"column:carried_at"`
	Version       int        `gorm:"column:version;not null"`
	UpdatedBy     guid.ID    `gorm:"column:updated_by;not null"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (WorkPlanning) TableName() string { return "delivery_work_planning" }

// PlanningCarryover is immutable provenance for period report drafts.
type PlanningCarryover struct {
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID        guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID      guid.ID   `gorm:"column:work_item_id;not null"`
	PrincipalID     guid.ID   `gorm:"column:principal_id;not null"`
	FromIterationID guid.ID   `gorm:"column:from_iteration_id;not null"`
	ToIterationID   *guid.ID  `gorm:"column:to_iteration_id"`
	CarriedAt       time.Time `gorm:"column:carried_at;not null;autoCreateTime:false"`
}

func (PlanningCarryover) TableName() string { return "delivery_planning_carryovers" }
