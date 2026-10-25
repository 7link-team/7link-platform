package governance

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Scheduled records reserve the existing unique delegation slot, but carry no
// installed IAM bindings until activated. Existing SQL lifecycle invariants stay
// intact; SCHEDULED is the public view of this inactive authority record.
func pendingDelegation(row *model.Delegation) bool {
	return row.State == "ACTIVE" && row.ActivatedAt == nil && row.StartsAt.After(row.CreatedAt)
}

func (m *Module) activateScheduledDelegations(ctx context.Context, job jobs.Job) error {
	if job.TenantID != 0 {
		return jobs.Permanent(ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	q := query.BindDelegations(db, m.seed.names)
	var after guid.ID
	for {
		rows, err := q.WithContext(ctx).Where(q.ID.Gt(after), q.State.Eq("ACTIVE"), q.ActivatedAt.IsNull(), q.StartsAt.Lte(m.seed.now()), q.EndsAt.Gt(m.seed.now())).Order(q.ID).Limit(100).Find()
		if err != nil {
			return err
		}
		for _, row := range rows {
			after = row.ID
			if !pendingDelegation(row) {
				continue
			}
			if err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.activateScheduledDelegationOn(ctx, tx, row) }); err != nil {
				return err
			}
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

func (m *Module) activateScheduledDelegationOn(ctx context.Context, tx *gorm.DB, candidate *model.Delegation) error {
	var conditions DelegationConditions
	if json.Unmarshal([]byte(candidate.ConditionsJSON), &conditions) != nil || !validDelegationConditions(conditions) {
		return ErrUnavailable
	}
	// Re-evaluate the actual grantor under current principal/project locks. This
	// context is used only for validation; activation events retain SYSTEM actor.
	grantor := authn.WithClaims(ctx, &authn.Claims{TenantID: candidate.TenantID, PrincipalID: candidate.GrantorPrincipalID, SubjectType: authn.SubjectTypePrincipal})
	in := DelegationGrantInput{ProjectID: candidate.ProjectID, GranteePrincipalID: candidate.GranteePrincipalID, DelegationConditions: conditions, DurationMinutes: int(candidate.EndsAt.Sub(candidate.StartsAt) / time.Minute), Reason: candidate.Reason}
	agent, err := m.validateDelegationGrantOn(grantor, tx, candidate.TenantID, in)
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrInvalidArgument) {
		// No authority is installed. A later tick can retry restored eligibility;
		// the original end time and explicit cancellation remain authoritative.
		return nil
	}
	if err != nil {
		return err
	}
	if agent != candidate.GranteeAgentID {
		return nil
	}
	project, err := m.delegations.projects.InspectOn(grantor, tx, candidate.TenantID, candidate.ProjectID)
	if err != nil {
		return err
	}
	if !project.DelegatedAllowed {
		return nil
	}
	q := query.BindDelegations(tx, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(candidate.TenantID), q.ID.Eq(candidate.ID)).Clauses(clause.Locking{Strength: "UPDATE"}).Take()
	if err != nil {
		return err
	}
	now := m.seed.now()
	if !pendingDelegation(row) || row.StartsAt.After(now) || !row.EndsAt.After(now) {
		return nil
	}
	var bindings []DelegationBinding
	if json.Unmarshal([]byte(row.IAMBindings), &bindings) != nil || len(bindings) == 0 {
		return ErrUnavailable
	}
	for _, binding := range bindings {
		role, err := m.roleIDOn(ctx, tx, row.TenantID, binding.RoleKey)
		if err != nil {
			return err
		}
		if role != binding.RoleID {
			return ErrUnavailable
		}
		if _, _, err = m.delegations.bindings.PutOn(ctx, tx, row.TenantID, row.ProjectID, role, row.GranteePrincipalID, core.BindingAllow, row.EndsAt); err != nil {
			return err
		}
	}
	changed, err := q.WithContext(ctx).Where(q.ID.Eq(row.ID), q.Version.Eq(row.Version), q.State.Eq("ACTIVE"), q.ActivatedAt.IsNull()).Updates(map[string]any{"activated_at": now, "version": row.Version + 1, "updated_at": now})
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrVersion
	}
	row.ActivatedAt = &now
	row.Version++
	return m.delegationEventOn(ctx, tx, row, "delegation.activated", "Scheduled delegation became effective", "")
}
