package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestWorkflowGuardRollsBackCanonicalClaim(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	deny := true
	seen := 0
	require.NoError(t, m.installWorkflow(db, func(ctx context.Context, tx *gorm.DB, r WorkflowTransition) error {
		seen++
		require.EqualValues(t, 1, r.TenantID)
		require.EqualValues(t, 99, r.ProjectID)
		require.Equal(t, work.ID, r.WorkItemID)
		require.Equal(t, "READY", r.From)
		require.Equal(t, "CLAIMED", r.To)
		ok, e := r.FactOn("REQUIRED_FIELDS", "assignee", "")
		require.NoError(t, e)
		require.True(t, ok, "same-transaction assignment is visible")
		if deny {
			return ErrWorkPreparationState
		}
		return nil
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove("delivery:workflow_transition")) })
	q := query.BindWorkTransitions(db, m.service.names)
	before, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	err := db.Transaction(func(tx *gorm.DB) error {
		_, e := m.claimHumanWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, work.Version, assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11})
		return e
	})
	require.ErrorIs(t, err, ErrWorkPreparationState)
	require.Equal(t, 1, seen)
	a := query.BindAssignments(db, m.service.names)
	count, e := a.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, count)
	current, e := m.readWork(ctx, db, 1, 99, work.ID, 11)
	require.NoError(t, e)
	require.Equal(t, work.Version, current.Version)
	require.Equal(t, "READY", current.State)
	count, e = q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Equal(t, before, count)
	deny = false
	assignmentClaim(t, m, db, ctx, work, false)
	require.Equal(t, 2, seen)
	require.Error(t, m.installWorkflow(db, func(context.Context, *gorm.DB, WorkflowTransition) error { return errors.New("second binding") }), "duplicate installation must not replace the guard")
}

func TestSpawnAssignmentUsesPersistedTenantProjectScope(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	claimed := assignmentClaim(t, m, db, ctx, work, false)
	_, _, err := m.spawnAssignmentOn(ctx, db, 1, 99, claimed.Assignment.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		agent, found, err := m.spawnAssignmentOn(ctx, tx, 1, 99, claimed.Assignment.ID)
		require.NoError(t, err)
		require.True(t, found)
		require.Zero(t, agent, "Human assignments are not Agent execution costs")
		for _, ref := range [][3]guid.ID{{2, 99, claimed.Assignment.ID}, {1, 98, claimed.Assignment.ID}, {1, 99, claimed.Assignment.ID + 1}} {
			_, found, err = m.spawnAssignmentOn(ctx, tx, ref[0], ref[1], ref[2])
			require.NoError(t, err)
			require.False(t, found)
		}
		return nil
	}))
}

func TestWorkItemReferenceUsesCurrentTenantProjectAndArchive(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.workItemReferenceOn(ctx, tx, 1, 99, work.ID))
		for _, r := range [][3]guid.ID{{2, 99, work.ID}, {1, 98, work.ID}, {1, 99, work.ID + 1}} {
			require.ErrorIs(t, m.workItemReferenceOn(ctx, tx, r[0], r[1], r[2]), ErrProjectNotFound)
		}
		return nil
	}))
	require.ErrorIs(t, m.workItemReferenceOn(ctx, db, 1, 99, work.ID), ErrInvalidArgument)
}

func TestEnsembleWorkAuthorityPreservesOwnerScopeAndTenantProjectFences(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	allow := true
	m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.EqualValues(t, 11, actor)
		require.Contains(t, []string{"work_item_read", "work_item_assign"}, permission)
		if !allow {
			return authz.DataConstraint{}, ErrWorkForbidden
		}
		return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
	}
	for _, permission := range []string{"work_item_read", "work_item_assign"} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ensembleWorkOn(ctx, tx, 1, 99, work.ID, permission) }))
	}
	allow = false
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, m.ensembleWorkOn(ctx, tx, 1, 99, work.ID, "work_item_assign"), fleet.ErrMachineForbidden)
		return nil
	}))
	allow = true
	for _, ref := range [][3]guid.ID{{2, 99, work.ID}, {1, 98, work.ID}, {1, 99, work.ID + 1}} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			err := m.ensembleWorkOn(ctx, tx, ref[0], ref[1], ref[2], "work_item_read")
			if ref[0] != 1 {
				require.ErrorIs(t, err, fleet.ErrMachineForbidden)
			} else {
				require.ErrorIs(t, err, fleet.ErrMachineNotFound)
			}
			return nil
		}))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, m.ensembleWorkOn(ctx, tx, 1, 99, work.ID, "execution_start"), fleet.ErrInvalidArgument)
		return nil
	}))
	_, err := query.BindWorkItems(db, m.service.names).WithContext(ctx).Where(query.BindWorkItems(db, m.service.names).ID.Eq(work.ID)).Update(query.BindWorkItems(db, m.service.names).CreatedBy, guid.ID(12))
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.Error(t, m.ensembleWorkOn(ctx, tx, 1, 99, work.ID, "work_item_assign"), "owner-scoped grant must not reach a work item created by another principal")
		return nil
	}))
	other := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	require.ErrorIs(t, m.ensembleWorkOn(other, db, 1, 99, work.ID, "work_item_read"), fleet.ErrMachineForbidden)
}

func TestEnsembleClosedOrRemovedProjectIsIneligible(t *testing.T) {
	for _, state := range []string{"CLOSED", "CANCELLED", "ARCHIVED"} { //nolint:misspell // Frozen persisted protocol state.
		t.Run(state, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			p := query.BindProjects(db, m.service.names)
			if state == "ARCHIVED" {
				_, err := p.WithContext(ctx).Where(p.TenantID.Eq(guid.ID(1)), p.ProjectID.Eq(guid.ID(99))).UpdateSimple(p.Stage.Value("CLOSED"), p.ArchivedAt.Value(time.Now().UTC()))
				require.NoError(t, err)
			} else {
				_, err := p.WithContext(ctx).Where(p.TenantID.Eq(guid.ID(1)), p.ProjectID.Eq(guid.ID(99))).Update(p.Stage, state)
				require.NoError(t, err)
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				err := m.ensembleWorkOn(ctx, tx, 1, 99, work.ID, "work_item_read")
				if state == "ARCHIVED" {
					require.ErrorIs(t, err, fleet.ErrMachineNotFound)
				} else {
					require.ErrorIs(t, err, fleet.ErrMachineForbidden)
				}
				return nil
			}))
		})
	}
}

func TestClaimFactsUseOnlyCurrentPersistedResult(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		facts, found, e := h.m.claimEvaluationOn(h.ctx, tx, 1, 99, h.work, result.ID)
		require.NoError(t, e)
		require.True(t, found)
		require.Equal(t, result.ID, facts.ClaimID)
		require.Equal(t, h.work, facts.WorkItemID)
		require.Nil(t, facts.SelfConfidence)
		for _, ref := range [][4]guid.ID{{2, 99, h.work, result.ID}, {1, 98, h.work, result.ID}, {1, 99, h.work, result.ID + 1}, {1, 99, h.work + 1, result.ID}} {
			_, found, e = h.m.claimEvaluationOn(h.ctx, tx, ref[0], ref[1], ref[2], ref[3])
			require.NoError(t, e)
			require.False(t, found)
		}
		return nil
	}))
	_, _, err = h.m.claimEvaluationOn(context.Background(), h.db, 1, 99, h.work, result.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = h.m.claimEvaluationOn(context.Background(), nil, 1, 99, h.work, result.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestDelegationProjectFactsAreCurrentAndTenantBound(t *testing.T) {
	m, db, ctx, _ := workHarness(t)
	q := query.BindProjects(db, m.service.names)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		f, e := m.delegationProjectOn(ctx, tx, 1, 99)
		require.NoError(t, e)
		require.False(t, f.DelegatedAllowed)
		return nil
	}))
	_, e := q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).Updates(map[string]any{"team_mode": "AUTONOMOUS", "holds": "[]", "budget_state": "NORMAL", "budget_cap_micros": 1000000})
	require.NoError(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		f, e := m.delegationProjectOn(ctx, tx, 1, 99)
		require.NoError(t, e)
		require.True(t, f.DelegatedAllowed)
		_, e = m.delegationProjectOn(ctx, tx, 2, 99)
		require.ErrorIs(t, e, governance.ErrNotFound)
		return nil
	}))
	_, e = m.delegationProjectOn(ctx, db, 1, 99)
	require.ErrorIs(t, e, ErrInvalidArgument)
	s := module.NewServices()
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindDelegationProjects(s))
	s = module.NewServices()
	var facts governance.DelegationProjectFacts
	require.NoError(t, module.Provide[governance.DelegationProjectConnector](s, governance.DelegationProjectConnector{Bind: func(f governance.DelegationProjectFacts) error { facts = f; return nil }}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindDelegationProjects(s))
	require.NotNil(t, facts.InspectOn)
}

func TestHumanEscapeComesFromCanonicalFailedCheck(t *testing.T) {
	h := newResultFixture(t)
	require.NoError(t, h.m.installHumanEscape(h.db))
	result, e := h.submit()
	require.NoError(t, e)
	var emitted []events.Event
	fail := false
	h.m.service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
		if event.Type == "quality.escaped" {
			if fail {
				return 0, errors.New("outbox unavailable")
			}
			emitted = append(emitted, event)
		}
		return 1, nil
	})
	_, e = h.check(result.ID, 12, "FAILED")
	require.NoError(t, e)
	require.Empty(t, emitted, "no prior internal acceptance is not an escape")
	now := time.Now().UTC().Truncate(time.Millisecond)
	reviewer := guid.ID(12)
	run := model.VerificationRun{TenantID: 1, ID: 990, ProjectID: 99, WorkItemID: h.work, ResultID: result.ID, CriterionID: *h.input.Evidence[0].CriterionID, Method: "AGENT_JUDGE", Stage: "AGENT_ACCEPTANCE", Round: 1, State: "DONE", Verdict: "PASSED", ReviewerID: &reviewer, ReviewerFamily: "family-a", ReceiptHash: strings.Repeat("a", 64), CreatedAt: now, CompletedAt: &now}
	require.NoError(t, query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).Create(&run))
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	require.Empty(t, emitted)
	before := h.stored().Version
	var count int64
	require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Count(&count).Error)
	fail = true
	_, e = h.check(result.ID, 12, "FAILED")
	require.Error(t, e)
	require.Equal(t, before, h.stored().Version)
	var after int64
	require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Count(&after).Error)
	require.Equal(t, count, after, "action must roll back with outbox")
	fail = false
	_, e = h.check(result.ID, 12, "FAILED")
	require.NoError(t, e)
	require.Len(t, emitted, 1)
	data := emitted[0].Data.(map[string]any)
	require.Equal(t, "HUMAN_REVIEW", data["stage"])
	require.Len(t, data["passed_by"], 1)
	require.Equal(t, h.work, data["work_item_id"])
}

func TestWorkflowConnectorInstallsGuardOnActualRuntimeWriter(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	services := module.NewServices()
	require.NoError(t, m.provideWorkflowGovernance(services))
	require.NoError(t, services.Seal())
	c, e := module.Resolve[WorkflowTransitionConnector](services)
	require.NoError(t, e)
	require.ErrorIs(t, c.BindSelection(nil), ErrInvalidArgument)
	require.NoError(t, c.BindSelection(func(context.Context, *gorm.DB, WorkflowTransition) (string, error) { return "CLAIMED", nil }))
	require.ErrorIs(t, c.BindSelection(m.workflowSelection), ErrInvalidArgument)
	require.ErrorIs(t, c.Bind(nil), ErrUnavailable)
	failure := errors.New("effective workflow blocks claim")
	called := false
	require.NoError(t, c.Bind(func(_ context.Context, _ *gorm.DB, f WorkflowTransition) error {
		called = true
		require.Equal(t, work.ID, f.WorkItemID)
		return failure
	}))
	err := m.runtime.Database().Write().Transaction(func(tx *gorm.DB) error {
		_, e := m.claimHumanWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, work.Version, assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11})
		return e
	})
	require.ErrorIs(t, err, failure)
	require.True(t, called)
	var count int64
	require.NoError(t, db.Table(m.service.names.Assignments.String()).Count(&count).Error)
	require.Zero(t, count)
}

func TestWorkflowEvidenceAndDependencyFactsNeverTreatSourceFailureAsPass(t *testing.T) {
	m, db, ctx, work, predecessor := dependencyHarness(t)
	require.NoError(t, query.BindDependencies(db, m.service.names).WithContext(ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: work.ID, DependsOnID: predecessor.ID}))
	stored, e := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
	require.NoError(t, e)
	require.Equal(t, work.ID, stored.ID)
	failure := errors.New("workflow source unavailable")
	target := ""
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("workflow-fact-source", func(tx *gorm.DB) {
		if tx.Statement.Table == target {
			tx.AddError(failure)
		}
	}))
	defer db.Callback().Query().Remove("workflow-fact-source")
	for _, name := range []string{m.service.names.Dependencies.String(), m.service.names.WorkItems.String()} {
		target = name
		ok, e := m.workflowFactOn(ctx, db, stored, 11, "RELATED_OBJECTS", "ALL_DEPENDENCIES_DONE", "")
		require.ErrorIs(t, e, failure)
		require.False(t, ok)
	}
	target = ""
	ok, e := m.workflowFactOn(ctx, db, stored, 11, "EVIDENCE", "UNSUPPORTED_SOURCE", "")
	require.ErrorIs(t, e, ErrUnavailable)
	require.False(t, ok)
	claim := guid.ID(999999)
	stored.CurrentClaimID = &claim
	for _, rule := range []string{"LINKED_MR_MERGED", "DEPLOYMENT_CONTAINS_MERGE"} {
		ok, e := m.workflowFactOn(ctx, db, stored, 11, "EVIDENCE", rule, "")
		require.ErrorIs(t, e, ErrProjectNotFound)
		require.False(t, ok)
	}
}
