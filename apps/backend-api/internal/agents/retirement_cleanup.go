package agents

import (
	"context"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

const retirementCleanupTimeout = 5 * time.Minute

func (m *Module) prepareRetirementCleanupOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
	q := query.BindRetirement(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if r.State != "IN_PROGRESS" {
		return nil
	}
	if m.principals.Lock == nil {
		return ErrUnavailable
	}
	ids := []guid.ID{actorID(ctx), r.AgentPrincipalID}
	slices.Sort(ids)
	for _, principal := range slices.Compact(ids) {
		if principal <= 0 {
			return ErrForbidden
		}
		if err := m.principals.Lock(ctx, tx, principal); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) advanceRetirementCleanupOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version, agentVersion int) (model.Retirement, error) {
	var empty model.Retirement
	if err := m.prepareRetirementCleanupOn(ctx, tx, tenant, id); err != nil {
		return empty, err
	}
	r, err := m.retirementOn(ctx, tx, tenant, id, "agent_retire", true)
	if err != nil {
		return empty, err
	}
	p := r.Precheck.Progress
	if version < 1 || r.Version != version || r.State != "IN_PROGRESS" || r.Step != "CLEANUP" || p == nil {
		return empty, ErrConflict
	}
	if m.profiles.ReadOn == nil || m.retirementRuntime.RequestCleanupOn == nil || m.retirementRuntime.CleanupStateOn == nil || m.retirementRuntime.CompleteOn == nil || m.retirementRuntime.CompleteTimedOutOn == nil {
		return empty, ErrUnavailable
	}
	profile, err := m.profiles.ReadOn(ctx, tx, tenant, r.AgentPrincipalID)
	if err != nil {
		return empty, retirementRuntimeError(err)
	}
	if agentVersion > 0 && agentVersion != profile.Version {
		return empty, ErrConflict
	}
	var binding fleet.AgentRetirementBinding
	if p.CleanupStatus == "" {
		binding, err = m.retirementRuntime.RequestCleanupOn(ctx, tx, tenant, r.AgentPrincipalID, id, profile.Version)
		if err != nil {
			return empty, retirementRuntimeError(err)
		}
		if binding.State != "RETIRING" || binding.PrincipalID != r.AgentPrincipalID || binding.RetirementID != id || binding.Version <= profile.Version || binding.CleanupCommandID == nil {
			return empty, ErrUnavailable
		}
		p.CleanupStatus, r.AgentVersion, r.StepStartedAt = "PENDING", binding.Version, m.now()
	} else {
		state, err := m.retirementRuntime.CleanupStateOn(ctx, tx, tenant, r.AgentPrincipalID, id)
		if err != nil {
			return empty, retirementRuntimeError(err)
		}
		switch {
		case state == "ACKED":
			binding, err = m.retirementRuntime.CompleteOn(ctx, tx, tenant, r.AgentPrincipalID, id, profile.Version)
			p.CleanupStatus = "ACKED"
		case !m.now().Before(r.StepStartedAt.Add(retirementCleanupTimeout)):
			binding, err = m.retirementRuntime.CompleteTimedOutOn(ctx, tx, tenant, r.AgentPrincipalID, id, profile.Version)
			p.CleanupStatus = "TIMED_OUT"
		case state == "PENDING" || state == "FAILED":
			if p.CleanupStatus == state {
				return *r, nil
			}
			p.CleanupStatus = state
		default:
			return empty, ErrUnavailable
		}
		if err != nil {
			return empty, retirementRuntimeError(err)
		}
		if p.CleanupStatus == "ACKED" || p.CleanupStatus == "TIMED_OUT" {
			if binding.State != "RETIRED" || binding.PrincipalID != r.AgentPrincipalID || binding.RetirementID != id || binding.Version <= profile.Version {
				return empty, ErrUnavailable
			}
			now := m.now()
			r.State, r.Step, r.ActiveAgentID, r.CompletedAt, r.AgentVersion = "COMPLETED", "DONE", nil, &now, binding.Version
		}
	}
	if err = m.saveRetirementProgressOn(ctx, tx, r, version, "CLEANUP_PROGRESS"); err != nil {
		return empty, err
	}
	return *r, nil
}
