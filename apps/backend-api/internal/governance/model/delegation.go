package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Delegation is the domain authority record; IAM carrier bindings alone cannot
// authorize its use. ActiveKey is cleared only by a terminal transition.
type Delegation struct {
	ID                   guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID             guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID            guid.ID    `gorm:"column:project_id;not null"`
	GranteePrincipalID   guid.ID    `gorm:"column:grantee_principal_id;not null"`
	GranteeAgentID       guid.ID    `gorm:"column:grantee_agent_id;not null"`
	GrantorPrincipalID   guid.ID    `gorm:"column:grantor_principal_id;not null"`
	ConditionsJSON       string     `gorm:"column:conditions_json;not null"`
	IAMBindings          string     `gorm:"column:iam_bindings;not null"`
	ActivatedAt          *time.Time `gorm:"column:activated_at"`
	StartsAt             time.Time  `gorm:"column:starts_at;not null"`
	EndsAt               time.Time  `gorm:"column:ends_at;not null"`
	Reason               string     `gorm:"column:reason;size:2000;not null"`
	State                string     `gorm:"column:state;size:16;not null"`
	EndReason            *string    `gorm:"column:end_reason;size:32"`
	EndedAt              *time.Time `gorm:"column:ended_at"`
	EndedBy              *guid.ID   `gorm:"column:ended_by"`
	EndNote              *string    `gorm:"column:end_note;size:2000"`
	ActiveKey            *string    `gorm:"column:active_key;size:48"`
	ReplacesDelegationID *guid.ID   `gorm:"column:replaces_delegation_id"`
	Version              int        `gorm:"column:version;not null"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Delegation) TableName() string { return "governance_delegations" }
