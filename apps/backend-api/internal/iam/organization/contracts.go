// Package organization owns IAM tenants, departments, positions, groups, and
// their cross-module contracts.
package organization

import (
	"context"
	"encoding/json"
	"time"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// MembershipRule is the canonical dynamic-group predicate.
type MembershipRule json.RawMessage

// Group is the stable organization representation shared with provisioning.
type Group struct {
	ID          guid.ID        `json:"id"`
	TenantID    guid.ID        `json:"tenant_id"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Rule        MembershipRule `json:"membership_rule,omitempty"`
	Description string         `json:"description,omitempty"`
	Status      string         `json:"status"`
	SortOrder   int            `json:"sort_order"`
	Version     int64          `json:"version"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// GroupMember is one principal's effective membership in a group.
type GroupMember struct {
	GroupID     guid.ID    `json:"group_id"`
	PrincipalID guid.ID    `json:"principal_id"`
	DisplayName string     `json:"display_name"`
	Email       string     `json:"email,omitempty"`
	StartsAt    *time.Time `json:"starts_at,omitempty"`
	EndsAt      *time.Time `json:"ends_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ProvisionedGroupInput is the canonical transactional SCIM group command.
type ProvisionedGroupInput struct {
	DisplayName string
	Active      bool
	MemberIDs   []guid.ID
}

// GroupProvisioner is the organization capability consumed by SCIM.
type GroupProvisioner interface {
	CreateProvisionedTo(context.Context, *gorm.DB, guid.ID, ProvisionedGroupInput) (Group, error)
	ReplaceProvisionedTo(context.Context, *gorm.DB, guid.ID, guid.ID, ProvisionedGroupInput) (Group, error)
	DisableProvisionedTo(context.Context, *gorm.DB, guid.ID, guid.ID) (Group, error)
	Get(context.Context, guid.ID, guid.ID) (Group, error)
	ListMembers(context.Context, guid.ID, guid.ID) ([]GroupMember, error)
}
