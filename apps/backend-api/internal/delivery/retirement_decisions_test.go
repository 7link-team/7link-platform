package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementLeadPrecheckAndInvalidationAreSourceOwnedReplayableAndAtomic(t *testing.T) {
	for _, fault := range []string{"", "active", "audit"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _ := assignmentHarness(t, true)
			policy := m.workPolicy
			m.workPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
				if permission == "project_read" {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				return policy(ctx, tx, tenant, project, permission, actor)
			}
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				state := "RETIRING"
				if fault == "active" {
					state = "ACTIVE"
				}
				return fleet.AgentIdentity{ID: 77, TenantID: 1, PrincipalID: 12, State: state}, true, nil
			}
			p := query.BindProjects(db, m.service.names)
			_, err := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).Updates(map[string]any{"lead_agent_id": guid.ID(77), "dispatch_policy": "AUTO"})
			require.NoError(t, err)
			before, err := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).First()
			require.NoError(t, err)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				ids, err := m.retirementLeadProjectsOn(ctx, tx, 1, 12)
				require.NoError(t, err)
				require.Equal(t, []guid.ID{99}, ids)
				return nil
			}))
			if fault == "audit" {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			err = db.Transaction(func(tx *gorm.DB) error { return m.clearRetirementLeadsOn(ctx, tx, 1, 12) })
			after, e := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).First()
			require.NoError(t, e)
			if fault != "" {
				require.Error(t, err)
				require.Equal(t, before, after)
				return
			}
			require.NoError(t, err)
			require.Nil(t, after.LeadAgentID)
			require.Equal(t, "MANUAL", after.DispatchPolicy)
			require.Equal(t, before.Version+1, after.Version)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.clearRetirementLeadsOn(ctx, tx, 1, 12) }))
			replayed, e := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).First()
			require.NoError(t, e)
			require.Equal(t, after.Version, replayed.Version)
		})
	}
}

func TestRetirementLeadSourceFailuresNeverClearOwnership(t *testing.T) {
	for _, fault := range []string{"missing", "agent", "absent", "query", "update", "event"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _ := assignmentHarness(t, true)
			injected := errors.New("retirement source unavailable")
			want := injected
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 77, TenantID: 1, PrincipalID: 12, State: "RETIRING"}, true, nil
			}
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"lead_agent_id": 77, "dispatch_policy": "AUTO"}).Error)
			p := query.BindProjects(db, m.service.names)
			before, e := p.WithContext(ctx).Take()
			require.NoError(t, e)
			switch fault {
			case "missing":
				m.agents.ByPrincipalOn = nil
				want = ErrUnavailable
			case "agent":
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, injected
				}
			case "absent":
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, nil
				}
				want = ErrAssignmentState
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("retirement-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Projects.String() {
						tx.AddError(injected)
					}
				}))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("retirement-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Projects.String() {
						tx.AddError(injected)
					}
				}))
			case "event":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, injected })
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.clearRetirementLeadsOn(ctx, tx, 1, 12) }), want)
			if fault != "update" && fault != "event" {
				err := db.Transaction(func(tx *gorm.DB) error {
					ids, e := m.retirementLeadProjectsOn(ctx, tx, 1, 12)
					require.Empty(t, ids)
					return e
				})
				if fault == "absent" {
					require.ErrorIs(t, err, ErrInvalidMember)
				} else {
					require.ErrorIs(t, err, want)
				}
			}
			require.NoError(t, db.Callback().Query().Remove("retirement-failure"))
			require.NoError(t, db.Callback().Update().Remove("retirement-failure"))
			after, e := p.WithContext(ctx).Take()
			require.NoError(t, e)
			require.Equal(t, before, after)
		})
	}
}

func TestRetirementReleasesRetiredOwnershipWithoutWithdrawingSubmittedClaim(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	claimed := assignmentClaim(t, m, db, ctx, work, false)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 77, PrincipalID: 12, State: "RETIRED"}, true, nil
	}
	m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
	}
	a, w := query.BindAssignments(db, m.service.names), query.BindWorkItems(db, m.service.names)
	_, err := a.WithContext(ctx).Where(a.ID.Eq(claimed.Assignment.ID)).Updates(map[string]any{"assignee_kind": "AGENT", "assignee_principal_id": guid.ID(12), "agent_id": guid.ID(77)})
	require.NoError(t, err)
	_, err = w.WithContext(ctx).Where(w.ID.Eq(work.ID)).Updates(map[string]any{"current_claim_id": guid.ID(501), "state": "NEEDS_HUMAN_REVIEW"})
	require.NoError(t, err)
	item := RetirementAssignment{AssignmentID: claimed.Assignment.ID, ProjectID: 99, WorkItemID: work.ID, WorkVersion: claimed.WorkItem.Version, AssignmentVersion: claimed.Assignment.Version}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.releaseRetirementAssignmentOn(ctx, tx, 1, 12, 900, item) }))
	current, err := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "NEEDS_HUMAN_REVIEW", current.State)
	require.EqualValues(t, 501, *current.CurrentClaimID)
	assignment, err := a.WithContext(ctx).Where(a.ID.Eq(item.AssignmentID)).First()
	require.NoError(t, err)
	require.Equal(t, "RELEASED", assignment.Status)
	require.Nil(t, assignment.ActiveSlot)
}

func TestRetirementUnknownExecutionDetachHasNoResultAndRollsBackWithAudit(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "audit_failure"}[denied], func(t *testing.T) {
			h, l := executionWorkFixture(t)
			policy := h.m.workPolicy
			h.m.workPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
				if permission == "execution_stop" {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				return policy(ctx, tx, tenant, project, permission, actor)
			}
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				if err := h.m.authorizeExecutionWorkOn(h.ctx, tx, l, "CREATE"); err != nil {
					return err
				}
				return h.m.startExecutionWorkOn(h.ctx, tx, l)
			}))
			if denied {
				h.m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.interruptRetirementExecutionOn(h.ctx, tx, l) })
			w, e := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).First()
			require.NoError(t, e)
			if denied {
				require.Error(t, err)
				require.Equal(t, &l.ExecutionID, w.CurrentExecutionID)
				require.Equal(t, "IN_PROGRESS", w.State)
			} else {
				require.NoError(t, err)
				require.Nil(t, w.CurrentExecutionID)
				require.Equal(t, "CLAIMED", w.State)
			}
			require.Nil(t, w.CurrentClaimID)
			count, e := query.BindResultVersions(h.db, h.m.service.names).WithContext(h.ctx).Count()
			require.NoError(t, e)
			require.Zero(t, count)
		})
	}
}

func TestRetirementEnsembleUnknownPreservesOtherExecutionAndClaim(t *testing.T) {
	h, l := executionWorkFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	plan := guid.ID(880)
	a := query.BindAssignments(h.db, h.m.service.names)
	_, err := a.WithContext(h.ctx).Where(a.ID.Eq(l.AssignmentID)).Updates(map[string]any{"ensemble_plan_id": plan, "active_slot": nil})
	require.NoError(t, err)
	w := query.BindWorkItems(h.db, h.m.service.names)
	_, err = w.WithContext(h.ctx).Where(w.ID.Eq(l.WorkItemID)).Updates(map[string]any{"state": "IN_PROGRESS", "current_execution_id": guid.ID(881), "current_claim_id": guid.ID(882)})
	require.NoError(t, err)
	h.m.ensembleBranchCheck = func(_ context.Context, _ *gorm.DB, tenant, project, work, actualPlan, assignment, execution guid.ID) error {
		require.Equal(t, []guid.ID{l.TenantID, l.ProjectID, l.WorkItemID, plan, l.AssignmentID, l.ExecutionID}, []guid.ID{tenant, project, work, actualPlan, assignment, execution})
		return nil
	}
	before, err := w.WithContext(h.ctx).Where(w.ID.Eq(l.WorkItemID)).First()
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.interruptRetirementExecutionOn(h.ctx, tx, l) }))
	after, err := w.WithContext(h.ctx).Where(w.ID.Eq(l.WorkItemID)).First()
	require.NoError(t, err)
	require.Equal(t, before, after, "a branch retirement must not mutate another attempt or submitted result")
	h.m.ensembleBranchCheck = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) error {
		return ErrAssignmentHolder
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.interruptRetirementExecutionOn(h.ctx, tx, l) }), ErrAssignmentHolder)
}

func TestRetirementReclaimsRetiredEnsembleOwnershipPreservingOtherBranches(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	claimed := assignmentClaim(t, m, db, ctx, work, false)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	state := "RETIRING"
	m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 77, PrincipalID: 12, State: state}, true, nil
	}
	m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
	}
	a, w := query.BindAssignments(db, m.service.names), query.BindWorkItems(db, m.service.names)
	_, err := a.WithContext(ctx).Where(a.ID.Eq(claimed.Assignment.ID)).Updates(map[string]any{"assignee_kind": "AGENT", "assignee_principal_id": guid.ID(12), "agent_id": guid.ID(77), "ensemble_plan_id": guid.ID(880), "active_slot": nil})
	require.NoError(t, err)
	_, err = w.WithContext(ctx).Where(w.ID.Eq(work.ID)).Updates(map[string]any{"current_execution_id": guid.ID(881), "current_claim_id": guid.ID(882), "state": "IN_PROGRESS"})
	require.NoError(t, err)
	item := RetirementAssignment{AssignmentID: claimed.Assignment.ID, ProjectID: 99, WorkItemID: work.ID, WorkVersion: claimed.WorkItem.Version, AssignmentVersion: claimed.Assignment.Version}
	release := func() error {
		return db.Transaction(func(tx *gorm.DB) error { return m.releaseRetirementAssignmentOn(ctx, tx, 1, 12, 900, item) })
	}
	require.Error(t, release(), "ordinary transfer/release must retain Heavy guards")
	state = "RETIRED"
	require.NoError(t, release())
	current, err := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "IN_PROGRESS", current.State)
	require.EqualValues(t, 881, *current.CurrentExecutionID)
	require.EqualValues(t, 882, *current.CurrentClaimID)
	row, err := a.WithContext(ctx).Where(a.ID.Eq(item.AssignmentID)).First()
	require.NoError(t, err)
	require.Equal(t, "RELEASED", row.Status)
}

func TestRetirementDecisionsFiltersAuthorTimeAndCurrentReadAuthority(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	r := query.BindResultVersions(db, m.service.names)
	a := query.BindResultActions(db, m.service.names)
	for i := 0; i < 3; i++ {
		author, at := guid.ID(12), now
		if i == 1 {
			author = 13
		}
		if i == 2 {
			at = now.Add(-31 * 24 * time.Hour)
		}
		result := &model.ResultVersion{ID: guid.ID(700 + i), TenantID: 1, ProjectID: 99, WorkItemID: work.ID, Sequence: i + 1, ActorID: author, BaselineVersionNo: 1, Summary: "real result", VerificationPlan: "{}", CreatedAt: at}
		require.NoError(t, r.WithContext(ctx).Create(result))
		action := &model.ResultAction{ID: guid.ID(800 + i), TenantID: 1, ProjectID: 99, WorkItemID: work.ID, ResultID: result.ID, Kind: "RETURN", Verdict: "FAILED", ActorID: 11, AuthoritySource: "TEST", Reason: "actual rejection", CreatedAt: at}
		require.NoError(t, a.WithContext(ctx).Create(action))
	}
	read := func() ([]RetirementDecision, error) {
		var rows []RetirementDecision
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			rows, err = m.retirementDecisionsOn(ctx, tx, 1, 12, now.Add(-30*24*time.Hour))
			return err
		})
		return rows, err
	}
	rows, err := read()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 800, rows[0].ID)
	require.Equal(t, "actual rejection", rows[0].Reason)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	rows, err = read()
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = m.retirementDecisionsOn(ctx, db, 1, 12, now)
	require.ErrorIs(t, err, ErrInvalidArgument)
}
