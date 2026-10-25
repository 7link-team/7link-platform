package delivery

import (
	"context"
	"slices"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) bindEnsembleRuntime(s *module.Services, c fleet.EnsembleConnector) error {
	machines, e := module.Resolve[fleet.MachineDirectory](s)
	if e != nil {
		return e
	}
	if c.CheckBranchOn == nil || machines.ByIDOn == nil {
		return ErrUnavailable
	}
	m.ensembleBranchCheck = c.CheckBranchOn
	return c.BindRuntime(fleet.EnsembleRuntime{
		LockOn: func(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, branches []fleet.EnsembleLaunchBranch) error {
			if m.service == nil || m.principals.Lock == nil {
				return ErrUnavailable
			}
			q := query.BindWorkItems(tx, m.service.names)
			seed, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(work), q.ArchivedAt.IsNull()).First()
			if e != nil {
				return e
			}
			ids := []guid.ID{authn.PrincipalIDFromContext(ctx), seed.AccountablePrincipalID}
			if m.executionHolds.PrincipalsOn == nil {
				return ErrUnavailable
			}
			// Cancellation and approval may be requested by a different Human
			// than the launcher. Include persisted execution initiators before
			// acquiring project/work locks, as ordinary execution controls do.
			participants, e := m.executionHolds.PrincipalsOn(ctx, tx, tenant, project, work)
			if e != nil {
				return e
			}
			ids = append(ids, participants...)
			for _, b := range branches {
				f, e := machines.ByIDOn(ctx, tx, tenant, b.MachineID)
				if e != nil {
					return e
				}
				ids = append(ids, b.PrincipalID, f.PrincipalID)
			}
			slices.Sort(ids)
			ids = slices.Compact(ids)
			for _, id := range ids {
				if id <= 0 {
					return ErrInvalidMember
				}
				if e = m.principals.Lock(ctx, tx, id); e != nil {
					return e
				}
			}
			// Recheck the owner after acquiring all participating principal locks.
			actual, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(work)).First()
			if e != nil {
				return e
			}
			if actual.AccountablePrincipalID != seed.AccountablePrincipalID {
				return ErrWorkVersion
			}
			return nil
		}, AssignOn: m.assignEnsembleOn})
}
func (m *Module) assignEnsembleOn(ctx context.Context, tx *gorm.DB, tenant, project, work, plan guid.ID, branches []fleet.EnsembleLaunchBranch) error {
	p := query.BindProjects(tx, m.service.names)
	projectRow, e := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project)).First()
	if e != nil {
		return e
	}
	if len(branches) > projectRow.EnsembleMaxBranches {
		return ErrAssignmentState
	}
	a := clientWork{m: m}
	for _, b := range branches {
		facts, e := a.dispatchWorkOn(ctx, tx, tenant, project, work)
		if e != nil {
			return e
		}
		_, e = a.dispatchApplyOn(ctx, tx, tenant, project, DispatchAssignmentInput{WorkItemID: work, AssigneePrincipalID: b.PrincipalID, ExpectedWorkVersion: facts.WorkVersion, Reason: "Approved ensemble branch", EnsemblePlanID: plan, EnsembleAssignmentID: b.AssignmentID, EnsembleExecutionID: b.ExecutionID})
		if e != nil {
			return e
		}
	}
	return nil
}

// The source proves that this exact assignment/execution pair belongs to the
// approved snapshot. Merely setting ensemble_plan_id never relaxes a guard.
func (m *Module) checkHeavyExecutionOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, plan *guid.ID) error {
	if plan == nil || *plan <= 0 || m.ensembleBranchCheck == nil {
		return ErrAssignmentHolder
	}
	return m.ensembleBranchCheck(ctx, tx, l.TenantID, l.ProjectID, l.WorkItemID, *plan, l.AssignmentID, l.ExecutionID)
}
