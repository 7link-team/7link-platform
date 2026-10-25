package delivery

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDispatchCanonicalAssignmentAndReassignmentPreserveHistory(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, nil
	}
	m.deliveryAuthority = func(_ context.Context, _ *gorm.DB, in governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		require.Equal(t, "work_item_assign", in.Code)
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:dispatch"}, nil
	}
	a := clientWork{m: m}
	var first guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		fact, err := a.dispatchWorkOn(ctx, tx, 1, 99, work.ID)
		require.NoError(t, err)
		require.Equal(t, work.Version, fact.WorkVersion)
		require.Equal(t, "READY", fact.State)
		first, err = a.dispatchApplyOn(ctx, tx, 1, 99, DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 501, AssigneePrincipalID: 11, ExpectedWorkVersion: fact.WorkVersion, Reason: "Current qualified Human"})
		return err
	}))
	q := query.BindAssignments(db, m.service.names)
	assignment, err := q.WithContext(ctx).Where(q.ID.Eq(first)).First()
	require.NoError(t, err)
	require.Equal(t, "HUMAN", assignment.AssigneeKind)
	require.Nil(t, assignment.AgentID)
	require.Equal(t, work.ID, *assignment.ActiveSlot)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		loads, e := a.dispatchLoadsOn(ctx, tx, 1, 99, []guid.ID{12, 11, 11})
		require.NoError(t, e)
		require.Len(t, loads, 2)
		require.Equal(t, guid.ID(11), loads[0].PrincipalID)
		require.Equal(t, 1, loads[0].ActiveItems)
		require.True(t, loads[0].Member)
		_, e = a.dispatchApplyOn(ctx, tx, 1, 99, DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 502, AssigneePrincipalID: 12, ExpectedWorkVersion: work.Version + 1, Reason: "Human confirmed reassignment", Reassign: true})
		return e
	}))
	history, err := q.WithContext(ctx).Where(q.ID.Eq(first)).First()
	require.NoError(t, err)
	require.Equal(t, "RELEASED", history.Status)
	require.Nil(t, history.ActiveSlot)
	require.NotNil(t, history.EndedAt)
	require.Equal(t, "DISPATCH_REASSIGNED", *history.EndReason)
	active, err := q.WithContext(ctx).Where(q.Status.Eq("ACTIVE")).Find()
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, guid.ID(12), active[0].AssigneePrincipalID)
	stored, err := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
	require.NoError(t, err)
	require.Equal(t, "CLAIMED", stored.State)
	require.Nil(t, stored.CurrentClaimID)
	require.Nil(t, stored.CurrentExecutionID)
}

func TestDispatchFailureBoundariesPreserveCanonicalWork(t *testing.T) {
	for _, scenario := range []string{"invalid", "version", "stage", "held", "budget", "container", "stale", "claim", "execution", "member", "authority_missing", "authority_denied", "authority_error", "audit"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{}, false, nil
			}
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:dispatch"}, nil
			}
			input := DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 501, AssigneePrincipalID: 11, ExpectedWorkVersion: work.Version, Reason: "Assign current Human"}
			w, p := query.BindWorkItems(db, m.service.names), query.BindProjects(db, m.service.names)
			switch scenario {
			case "invalid":
				input.ProposalID = 0
			case "version":
				input.ExpectedWorkVersion++
			case "stage":
				_, e := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).Update(p.Stage, "PLANNING")
				require.NoError(t, e)
			case "held":
				_, e := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).Update(p.Holds, `[{"kind":"MANUAL"}]`)
				require.NoError(t, e)
			case "budget":
				_, e := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).Update(p.BudgetState, "EXHAUSTED")
				require.NoError(t, e)
			case "container":
				_, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).Update(w.IsContainer, true)
				require.NoError(t, e)
			case "stale":
				_, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).Update(w.BaselineStale, true)
				require.NoError(t, e)
			case "claim":
				_, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).Update(w.CurrentClaimID, 999)
				require.NoError(t, e)
			case "execution":
				_, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).Update(w.CurrentExecutionID, 999)
				require.NoError(t, e)
			case "member":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
			case "authority_missing":
				m.deliveryAuthority = nil
			case "authority_denied":
				m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{}, nil
				}
			case "authority_error":
				m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{}, errors.New("authority unavailable")
				}
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			before, err := w.WithContext(ctx).First()
			require.NoError(t, err)
			err = db.Transaction(func(tx *gorm.DB) error {
				_, e := (clientWork{m: m}).dispatchApplyOn(ctx, tx, 1, 99, input)
				return e
			})
			require.Error(t, err)
			after, err := w.WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, before, after)
			count, err := query.BindAssignments(db, m.service.names).WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

func TestClientProjectReadScopeAndNonDelegablePreviewApproval(t *testing.T) {
	m, db, ctx, _ := assignmentHarness(t, true)
	p := query.BindProjects(db, m.service.names)
	_, err := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).Update(p.CreatedBy, guid.ID(11))
	require.NoError(t, err)
	a := clientWork{m: m}
	for _, scope := range []authz.DataConstraint{{AllowAll: true}, {ResourceIDs: []string{"99"}}, {OwnerIDs: []string{"11"}}} {
		m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return scope, nil
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return a.LockProjectOn(ctx, tx, 1, 99, "project_read")
		}))
	}
	require.ErrorIs(t, a.LockProjectOn(ctx, db, 1, 99, "project_read"), ErrInvalidArgument)
	require.ErrorIs(t, a.LockProjectOn(ctx, nil, 1, 99, "project_read"), ErrInvalidArgument)
	for _, scenario := range []string{"policy_missing", "policy_error", "policy_denied", "authority_missing", "authority_error", "authority_denied", "delegated", "owner_grant", "role"} {
		m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{AllowAll: true}, nil
		}
		m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
			return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:reviewer"}, nil
		}
		failure := errors.New("current authority unavailable")
		switch scenario {
		case "policy_missing":
			m.workPolicy = nil
		case "policy_error":
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, failure
			}
		case "policy_denied":
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, nil
			}
		case "authority_missing":
			m.deliveryAuthority = nil
		case "authority_error":
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{}, failure
			}
		case "authority_denied":
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{}, nil
			}
		case "delegated":
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:reviewer", DelegationID: 1}, nil
			}
		case "owner_grant":
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "OWNER"}, nil
			}
		}
		err := db.Transaction(func(tx *gorm.DB) error { return a.ApprovePreviewShareOn(ctx, tx, 1, 99) })
		if scenario == "role" {
			require.NoError(t, err)
		} else {
			require.Error(t, err, scenario)
		}
		if scenario == "policy_missing" || scenario == "policy_error" || scenario == "policy_denied" {
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { return a.LockProjectOn(ctx, tx, 1, 99, "project_read") }))
		}
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		summary, err := a.ClientSummaryOn(ctx, tx, 1, 99)
		require.NoError(t, err)
		require.Equal(t, guid.ID(99), summary.ID)
		_, err = a.ClientSummaryOn(ctx, tx, 2, 99)
		require.Error(t, err)
		return nil
	}))
}
