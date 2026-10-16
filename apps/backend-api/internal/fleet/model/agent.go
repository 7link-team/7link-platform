// Package model owns fleet persistence records.
package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Agent is persisted by fleet.
type Agent struct {
	ID                      guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	PrincipalID             guid.ID    `gorm:"column:principal_id;not null"`
	Name                    string     `gorm:"column:name;not null;size:64"`
	Handle                  string     `gorm:"column:handle;not null;size:40"`
	Persona                 *string    `gorm:"column:persona;size:2000"`
	AvatarStyle             string     `gorm:"column:avatar_style;not null;size:32"`
	AvatarSeed              int64      `gorm:"column:avatar_seed;not null"`
	SystemPrompt            string     `gorm:"column:system_prompt;not null"`
	MachineID               *guid.ID   `gorm:"column:machine_id"`
	RuntimeKind             string     `gorm:"column:runtime_kind;not null;size:32"`
	ModelID                 string     `gorm:"column:model_id;not null;size:160"`
	ReasoningEffort         *string    `gorm:"column:reasoning_effort;size:16"`
	AuthorizationMode       string     `gorm:"column:authorization_mode;not null;size:32"`
	MaxContextTokens        int        `gorm:"column:max_context_tokens;not null"`
	MCPAllowlist            string     `gorm:"column:mcp_allowlist;not null"`
	CapabilityTags          string     `gorm:"column:capability_tags;not null"`
	DefaultChannelNames     string     `gorm:"column:default_channel_names;not null"`
	MemoryPolicy            string     `gorm:"column:memory_policy;not null"`
	MaxParallelItems        int        `gorm:"column:max_parallel_items;not null"`
	MaxRisk                 string     `gorm:"column:max_risk;not null;size:32"`
	State                   string     `gorm:"column:state;not null;size:32"`
	ProcessStatus           string     `gorm:"column:process_status;not null;size:32"`
	ProcessStatusAt         *time.Time `gorm:"column:process_status_at"`
	LastSeenAt              *time.Time `gorm:"column:last_seen_at"`
	ConfigRevision          int        `gorm:"column:config_revision;not null"`
	RunningConfigRevision   *int       `gorm:"column:running_config_revision"`
	AutoRestart             bool       `gorm:"column:auto_restart;not null"`
	RestartWindowStartedAt  *time.Time `gorm:"column:restart_window_started_at"`
	RestartCountInWindow    int        `gorm:"column:restart_count_in_window;not null"`
	RestartExhausted        bool       `gorm:"column:restart_exhausted;not null"`
	CredentialID            *guid.ID   `gorm:"column:credential_id"`
	CredentialDelivery      string     `gorm:"column:credential_delivery;not null;size:32"`
	PendingCleanupMachineID *guid.ID   `gorm:"column:pending_cleanup_machine_id"`
	RetirementID            *guid.ID   `gorm:"column:retirement_id"`
	RetiredAt               *time.Time `gorm:"column:retired_at"`
	Version                 int        `gorm:"column:version;not null"`
	Origin                  string     `gorm:"column:origin;not null;size:32"`
	CreatedByPrincipalID    guid.ID    `gorm:"column:created_by_principal_id;not null"`
	ParentAgentID           *guid.ID   `gorm:"column:parent_agent_id"`
	OwnerPrincipalID        guid.ID    `gorm:"column:owner_principal_id;not null"`
	SpawnGrantID            *guid.ID   `gorm:"column:spawn_grant_id"`
	SpawnRequestID          *guid.ID   `gorm:"column:spawn_request_id"`
	SpawnReason             *string    `gorm:"column:spawn_reason;size:2000"`
	ExpiresAt               *time.Time `gorm:"column:expires_at"`
	IdleSince               *time.Time `gorm:"column:idle_since"`
	AdoptedBy               *guid.ID   `gorm:"column:adopted_by"`
	AdoptedAt               *time.Time `gorm:"column:adopted_at"`
	OrphanedAt              *time.Time `gorm:"column:orphaned_at"`
	OrphanReason            *string    `gorm:"column:orphan_reason;size:32"`
	ModelFamily             string     `gorm:"column:model_family;not null;size:32"`
	TenantID                guid.ID    `gorm:"column:tenant_id;not null"`
	CreatedAt               time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt               time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	CreatedBy               *guid.ID   `gorm:"column:created_by"`
	UpdatedBy               *guid.ID   `gorm:"column:updated_by"`
}

func (Agent) TableName() string { return "fleet_agents" }

// AgentRole is persisted by fleet.
type AgentRole struct {
	AgentID    guid.ID    `gorm:"column:agent_id;primaryKey;autoIncrement:false;not null"`
	Role       string     `gorm:"column:role;primaryKey;autoIncrement:false;not null;size:32"`
	IAMRoleKey string     `gorm:"column:iam_role_key;not null;size:64"`
	RevokedAt  *time.Time `gorm:"column:revoked_at"`
	TenantID   guid.ID    `gorm:"column:tenant_id;not null"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt  time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	CreatedBy  *guid.ID   `gorm:"column:created_by"`
	UpdatedBy  *guid.ID   `gorm:"column:updated_by"`
}

func (AgentRole) TableName() string { return "fleet_agent_roles" }
