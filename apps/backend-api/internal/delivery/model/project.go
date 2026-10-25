// Package model contains persisted delivery business records.
package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Project belongs to the delivery module.
type Project struct {
	TenantID               guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ProjectID              guid.ID    `gorm:"column:project_id;primaryKey;autoIncrement:false"`
	Code                   string     `gorm:"column:code;size:16;not null"`
	ConstitutionVersionNo  *int       `gorm:"column:constitution_version_no"`
	Description            *string    `gorm:"column:description"`
	ClientEntityID         *guid.ID   `gorm:"column:client_entity_id"`
	Stage                  string     `gorm:"column:stage;not null"`
	StageChangedAt         time.Time  `gorm:"column:stage_changed_at;not null"`
	BlockedKind            *string    `gorm:"column:blocked_kind"`
	BlockedReason          *string    `gorm:"column:blocked_reason"`
	TeamMode               string     `gorm:"column:team_mode;not null"`
	TeamModeChangedAt      *time.Time `gorm:"column:team_mode_changed_at"`
	AutonomyVersion        *int64     `gorm:"column:autonomy_version"`
	LeadAgentID            *guid.ID   `gorm:"column:lead_agent_id"`
	DispatchPolicy         string     `gorm:"column:dispatch_policy;not null"`
	DispatchPolicyExplicit bool       `gorm:"column:dispatch_policy_explicit;not null"`
	EnsembleMaxBranches    int        `gorm:"column:ensemble_max_branches;not null"`
	OriginIntakeID         *guid.ID   `gorm:"column:origin_intake_id"`
	DefaultSendChannelID   *guid.ID   `gorm:"column:default_send_channel_id"`
	BudgetCapMicros        *int64     `gorm:"column:budget_cap_micros"`
	BudgetCapVersion       int        `gorm:"column:budget_cap_version;not null"`
	BudgetSpentMicros      int64      `gorm:"column:budget_spent_micros;not null"`
	BudgetNotifyPct        int        `gorm:"column:budget_notify_pct;not null"`
	BudgetState            string     `gorm:"column:budget_state;not null"`
	Holds                  string     `gorm:"column:holds;not null"`
	Health                 string     `gorm:"column:health;not null"`
	HealthReasons          string     `gorm:"column:health_reasons;not null"`
	HealthRefreshedAt      *time.Time `gorm:"column:health_refreshed_at"`
	PortalSettings         string     `gorm:"column:portal_settings;not null"`
	AcceptancePolicy       string     `gorm:"column:acceptance_policy;not null"`
	NextDeliverySeq        int        `gorm:"column:next_delivery_seq;not null"`
	TargetDate             *string    `gorm:"column:target_date"`
	StartedAt              *time.Time `gorm:"column:started_at"`
	DeliveredAt            *time.Time `gorm:"column:delivered_at"`
	ClosedAt               *time.Time `gorm:"column:closed_at"`
	CancelledAt            *time.Time `gorm:"column:cancelled_at"` //nolint:misspell // Spec 07 freezes this storage column spelling.
	CancelReason           *string    `gorm:"column:cancel_reason"`
	ProvisioningIssues     string     `gorm:"column:provisioning_issues;not null"`
	ArchivedAt             *time.Time `gorm:"column:archived_at"`
	Version                int        `gorm:"column:version;not null"`
	CreatedAt              time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt              time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	CreatedBy              *guid.ID   `gorm:"column:created_by"`
	UpdatedBy              *guid.ID   `gorm:"column:updated_by"`
}

func (Project) TableName() string { return "delivery_projects" }

// ProjectContact belongs to the delivery module.
type ProjectContact struct {
	TenantID           guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ProjectID          guid.ID   `gorm:"column:project_id;primaryKey;autoIncrement:false"`
	IntakeContactID    guid.ID   `gorm:"column:intake_contact_id;primaryKey;autoIncrement:false"`
	Role               string    `gorm:"column:role;not null"`
	ReceivesDeliveries bool      `gorm:"column:receives_deliveries;not null"`
	CanAccept          bool      `gorm:"column:can_accept;not null"`
	CreatedAt          time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt          time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	CreatedBy          *guid.ID  `gorm:"column:created_by"`
	UpdatedBy          *guid.ID  `gorm:"column:updated_by"`
}

func (ProjectContact) TableName() string { return "delivery_project_contacts" }

// ProjectRepository belongs to the delivery module.
type ProjectRepository struct {
	TenantID          guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID                guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	ProjectID         guid.ID    `gorm:"column:project_id;not null"`
	Name              string     `gorm:"column:name;size:100;not null"`
	RemoteURL         string     `gorm:"column:remote_url;size:2000;not null"`
	DefaultBranch     string     `gorm:"column:default_branch;size:200;not null"`
	ProtectedBranches string     `gorm:"column:protected_branches;not null"`
	IsPrimary         bool       `gorm:"column:is_primary;not null"`
	ArchivedAt        *time.Time `gorm:"column:archived_at"`
	Version           int        `gorm:"column:version;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt         time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	CreatedBy         *guid.ID   `gorm:"column:created_by"`
	UpdatedBy         *guid.ID   `gorm:"column:updated_by"`
}

func (ProjectRepository) TableName() string { return "delivery_project_repositories" }
