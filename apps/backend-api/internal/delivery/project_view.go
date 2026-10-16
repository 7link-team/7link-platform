package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/infra/guid"
	"encoding/json"
	"time"
)

// ProjectView is the public project record; persistence models stay private.
type ProjectView struct {
	TenantID               guid.ID         `json:"tenant_id"`
	ProjectID              guid.ID         `json:"project_id"`
	Code                   string          `json:"code"`
	Description            *string         `json:"description"`
	ClientEntityID         *guid.ID        `json:"client_entity_id"`
	Stage                  string          `json:"stage"`
	StageChangedAt         time.Time       `json:"stage_changed_at"`
	BlockedKind            *string         `json:"blocked_kind"`
	BlockedReason          *string         `json:"blocked_reason"`
	TeamMode               string          `json:"team_mode"`
	TeamModeChangedAt      *time.Time      `json:"team_mode_changed_at"`
	LeadAgentID            *guid.ID        `json:"lead_agent_id"`
	DispatchPolicy         string          `json:"dispatch_policy"`
	DispatchPolicyExplicit bool            `json:"dispatch_policy_explicit"`
	EnsembleMaxBranches    int             `json:"ensemble_max_branches"`
	OriginIntakeID         *guid.ID        `json:"origin_intake_id"`
	DefaultSendChannelID   *guid.ID        `json:"default_send_channel_id"`
	BudgetCapMicros        *int64          `json:"budget_cap_micros"`
	BudgetCapVersion       int             `json:"budget_cap_version"`
	BudgetSpentMicros      int64           `json:"budget_spent_micros"`
	BudgetNotifyPct        int             `json:"budget_notify_pct"`
	BudgetState            string          `json:"budget_state"`
	Holds                  json.RawMessage `json:"holds"`
	Health                 string          `json:"health"`
	HealthReasons          json.RawMessage `json:"health_reasons"`
	HealthRefreshedAt      *time.Time      `json:"health_refreshed_at"`
	PortalSettings         json.RawMessage `json:"portal_settings"`
	AcceptancePolicy       json.RawMessage `json:"acceptance_policy"`
	NextDeliverySeq        int             `json:"next_delivery_seq"`
	TargetDate             *string         `json:"target_date"`
	StartedAt              *time.Time      `json:"started_at"`
	DeliveredAt            *time.Time      `json:"delivered_at"`
	ClosedAt               *time.Time      `json:"closed_at"`
	CancelledAt            *time.Time      `json:"cancelled_at"` //nolint:misspell // Spec 07 freezes this API field spelling.
	CancelReason           *string         `json:"cancel_reason"`
	ProvisioningIssues     json.RawMessage `json:"provisioning_issues"`
	ArchivedAt             *time.Time      `json:"archived_at"`
	Version                int             `json:"version"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	CreatedBy              *guid.ID        `json:"created_by"`
	UpdatedBy              *guid.ID        `json:"updated_by"`
}
type projectEntityView struct {
	ID       guid.ID  `json:"id"`
	ParentID *guid.ID `json:"parent_id"`
}
type projectRoleBindingView struct {
	EntityID    guid.ID    `json:"entity_id"`
	RoleID      guid.ID    `json:"role_id"`
	PrincipalID guid.ID    `json:"principal_id"`
	Effect      string     `json:"effect"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
}
type projectCreateView struct {
	Project        ProjectView              `json:"project"`
	Entity         projectEntityView        `json:"entity"`
	ClientEntityID *guid.ID                 `json:"client_entity_id"`
	ChannelID      guid.ID                  `json:"channel_id"`
	RoleBindings   []projectRoleBindingView `json:"role_bindings"`
}

func projectCreatedView(created projectCreated) (projectCreateView, error) {
	p := created.project
	view := projectCreateView{Entity: projectEntityView{ID: created.entity.ID}, ClientEntityID: p.ClientEntityID, ChannelID: created.channelID, RoleBindings: make([]projectRoleBindingView, 0, len(created.bindings))}
	if created.entity.ParentID > 0 {
		parent := created.entity.ParentID
		view.Entity.ParentID = &parent
	}
	var err error
	view.Project, err = projectRecordView(p)
	if err != nil {
		return projectCreateView{}, err
	}
	for _, binding := range created.bindings {
		row := projectRoleBindingView{EntityID: binding.EntityID, RoleID: binding.RoleID, PrincipalID: binding.PrincipalID, Effect: string(binding.Effect), CreatedAt: binding.CreatedAt}
		if !binding.ExpiresAt.IsZero() {
			expiry := binding.ExpiresAt
			row.ExpiresAt = &expiry
		}
		view.RoleBindings = append(view.RoleBindings, row)
	}
	return view, nil
}

func projectRecordView(p model.Project) (ProjectView, error) {
	view := ProjectView{
		TenantID:               p.TenantID,
		ProjectID:              p.ProjectID,
		Code:                   p.Code,
		Description:            p.Description,
		ClientEntityID:         p.ClientEntityID,
		Stage:                  p.Stage,
		StageChangedAt:         p.StageChangedAt,
		BlockedKind:            p.BlockedKind,
		BlockedReason:          p.BlockedReason,
		TeamMode:               p.TeamMode,
		TeamModeChangedAt:      p.TeamModeChangedAt,
		LeadAgentID:            p.LeadAgentID,
		DispatchPolicy:         p.DispatchPolicy,
		DispatchPolicyExplicit: p.DispatchPolicyExplicit,
		EnsembleMaxBranches:    p.EnsembleMaxBranches,
		OriginIntakeID:         p.OriginIntakeID,
		DefaultSendChannelID:   p.DefaultSendChannelID,
		BudgetCapMicros:        p.BudgetCapMicros,
		BudgetCapVersion:       p.BudgetCapVersion,
		BudgetSpentMicros:      p.BudgetSpentMicros,
		BudgetNotifyPct:        p.BudgetNotifyPct,
		BudgetState:            p.BudgetState,
		Holds:                  json.RawMessage(p.Holds),
		Health:                 p.Health,
		HealthReasons:          json.RawMessage(p.HealthReasons),
		HealthRefreshedAt:      p.HealthRefreshedAt,
		PortalSettings:         json.RawMessage(p.PortalSettings),
		AcceptancePolicy:       json.RawMessage(p.AcceptancePolicy),
		NextDeliverySeq:        p.NextDeliverySeq,
		TargetDate:             p.TargetDate,
		StartedAt:              p.StartedAt,
		DeliveredAt:            p.DeliveredAt,
		ClosedAt:               p.ClosedAt,
		CancelledAt:            p.CancelledAt,
		CancelReason:           p.CancelReason,
		ProvisioningIssues:     json.RawMessage(p.ProvisioningIssues),
		ArchivedAt:             p.ArchivedAt,
		Version:                p.Version,
		CreatedAt:              p.CreatedAt,
		UpdatedAt:              p.UpdatedAt,
		CreatedBy:              p.CreatedBy,
		UpdatedBy:              p.UpdatedBy,
	}
	for _, value := range []json.RawMessage{view.Holds, view.HealthReasons, view.PortalSettings, view.AcceptancePolicy, view.ProvisioningIssues} {
		if !json.Valid(value) {
			return ProjectView{}, ErrUnavailable
		}
	}
	return view, nil
}
