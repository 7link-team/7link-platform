package execution

import (
	"context"
	"errors"
	"slices"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// RealtimeTopics authorizes content-less execution signals using the same
// current authority as GET execution. Signals never grant access to logs.
type RealtimeTopics struct {
	Authorize func(context.Context, guid.ID, guid.ID, guid.ID, bool) (bool, error)
}

func (m *Module) provideRealtimeTopics(s *module.Services) error {
	return module.Provide[RealtimeTopics](s, RealtimeTopics{Authorize: m.authorizeRealtime})
}

func (m *Module) authorizeRealtime(ctx context.Context, tenant, principal, id guid.ID, collection bool) (bool, error) {
	if collection || tenant <= 0 || principal <= 0 || id <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) != principal {
		return false, nil
	}
	db, err := m.writer()
	if err != nil {
		return false, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, e := m.rowOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		return m.humanAuthority(ctx, tx, tenant, row.ProjectID, "execution_read")
	})
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// validateAgentBindingOn rechecks actual fleet facts; a posted matching model
// alone cannot move an Agent's assignment to a different machine or runtime.
// Human assignments have no Agent binding and retain their existing behavior.
func (m *Module) validateAgentBindingOn(ctx context.Context, tx *gorm.DB, l WorkLink, machine guid.ID, plan ExecutionPlan) error {
	if m.work.AssignedAgentOn == nil {
		return nil
	}
	id, err := m.work.AssignedAgentOn(ctx, tx, l)
	if err != nil || id <= 0 {
		return err
	}
	if plan.ExecutorPrincipalID != id || plan.ModelFamily == "" {
		return ErrForbidden
	}
	if m.agents.ByPrincipalOn == nil {
		return ErrCapabilityUnavailable
	}
	agent, found, err := m.agents.ByPrincipalOn(ctx, tx, l.TenantID, id)
	if err != nil {
		return err
	}
	if !found || agent.PrincipalID != id || agent.State != "ACTIVE" || !slices.Contains(agent.Roles, "EXECUTOR") || agent.RuntimeKind != plan.Runtime || agent.ModelID != plan.Model || agent.ModelFamily != plan.ModelFamily {
		return ErrForbidden
	}
	if plan.AuthorizationMode == "FULL_AUTO" && agent.AuthorizationMode != "FULL_AUTO" {
		return ErrForbidden
	}
	if plan.AuthorizationMode == "ON_REQUEST" && agent.AuthorizationMode != "ON_REQUEST" {
		return ErrForbidden
	}
	if plan.AuthorizationMode == "WORKSPACE_WRITE" && agent.AuthorizationMode != "WORKSPACE_WRITE" {
		return ErrForbidden
	}
	if m.agentRuntime.ByPrincipalOn == nil {
		return ErrCapabilityUnavailable
	}
	fact, found, err := m.agentRuntime.ByPrincipalOn(ctx, tx, l.TenantID, id)
	if err != nil {
		return err
	}
	if !found || fact.MachineID == nil || *fact.MachineID != machine || fact.RuntimeKind != plan.Runtime {
		return ErrForbidden
	}
	if fact.MachineState != "ONLINE" {
		return ErrConflict
	}
	return nil
}
