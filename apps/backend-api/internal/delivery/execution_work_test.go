package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"math"
	"strings"
	"testing"
	"time"
)

func TestNotificationAffectedExecutorAndActiveEngineerAreScopedToCurrentAssignments(t *testing.T) {
	h := newResultFixture(t)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, query.BindRequirements(tx, h.m.service.names).WithContext(h.ctx).Create(&model.Requirement{TenantID: 1, ProjectID: 99, ID: 77, Number: 1, Title: "Baseline", Description: "", Priority: 1, Status: "DRAFT", Version: 1, CreatedAt: h.stored().CreatedAt, UpdatedAt: h.stored().UpdatedAt}))
		w := query.BindWorkItems(tx, h.m.service.names)
		_, err := w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).Updates(map[string]any{"requirement_id": 77, "state": "IN_PROGRESS"})
		require.NoError(t, err)
		for _, test := range []struct {
			tenant, subject, principal guid.ID
			want                       bool
		}{{1, 77, 11, true}, {2, 77, 11, false}, {1, 78, 11, false}, {1, 77, 12, false}} {
			allowed, err := h.m.requirementNotificationExecutorOn(h.ctx, tx, test.tenant, test.subject, test.principal)
			require.NoError(t, err)
			require.Equal(t, test.want, allowed)
		}
		allowed, err := h.m.activeNotificationEngineerOn(h.ctx, tx, 1, 99, 11)
		require.NoError(t, err)
		require.True(t, allowed)
		mode, err := h.m.notificationProjectModeOn(h.ctx, tx, 1, 99)
		require.NoError(t, err)
		require.Equal(t, "HYBRID", mode)
		a := query.BindAssignments(tx, h.m.service.names)
		_, err = a.WithContext(h.ctx).Where(a.WorkItemID.Eq(h.work)).Updates(map[string]any{"status": "EXPIRED", "active_slot": nil, "ended_at": h.stored().UpdatedAt, "end_reason": "LEASE_EXPIRED"})
		require.NoError(t, err)
		allowed, err = h.m.requirementNotificationExecutorOn(h.ctx, tx, 1, 77, 11)
		require.NoError(t, err)
		require.False(t, allowed)
		allowed, err = h.m.activeNotificationEngineerOn(h.ctx, tx, 1, 99, 11)
		require.NoError(t, err)
		require.False(t, allowed)
		return nil
	}))
}

func TestWorkNotificationFactsUseActualExecutorAndReviewSeparation(t *testing.T) {
	h := newResultFixture(t)
	_, err := h.submit()
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		facts, exists, err := h.m.workNotificationFactsOn(h.ctx, tx, 1, h.work)
		require.NoError(t, err)
		require.True(t, exists)
		require.Equal(t, guid.ID(11), facts.ExecutorID)
		_, exists, err = h.m.workNotificationFactsOn(h.ctx, tx, 2, h.work)
		require.NoError(t, err)
		require.False(t, exists)
		// Execution results may carry the Human initiator; an Agent assignment
		// must still select its assigner rather than invent a Human executor.
		a := query.BindAssignments(tx, h.m.service.names)
		assigner := guid.ID(13)
		_, err = a.WithContext(h.ctx).Where(a.WorkItemID.Eq(h.work)).Updates(map[string]any{"assignee_kind": "AGENT", "assignee_principal_id": 91, "agent_id": 93, "created_by": assigner})
		require.NoError(t, err)
		facts, exists, err = h.m.workNotificationFactsOn(h.ctx, tx, 1, h.work)
		require.NoError(t, err)
		require.True(t, exists)
		require.Zero(t, facts.ExecutorID)
		require.Equal(t, assigner, facts.AssignerID)
		h.m.deliveryAuthority = func(_ context.Context, _ *gorm.DB, in governance.AuthorityRequest) (governance.AuthorityDecision, error) {
			require.NotNil(t, in.Facts)
			return governance.AuthorityDecision{Allowed: !in.Facts.SelfProduced, AuthoritySource: "ROLE:reviewer"}, nil
		}
		for _, principal := range []guid.ID{11, 12} {
			eligible, err := h.m.workNotificationReviewEligibleOn(h.ctx, tx, 1, h.work, principal)
			require.NoError(t, err)
			require.Equal(t, principal != 11, eligible)
		}
		h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
			return governance.AuthorityDecision{}, nil
		}
		eligible, err := h.m.workNotificationReviewEligibleOn(h.ctx, tx, 1, h.work, 12)
		require.NoError(t, err)
		require.False(t, eligible)
		return nil
	}))
}

func TestProjectBudgetExactThresholdsHoldAndTransactionalPublication(t *testing.T) {
	s, db, draft := projectHarness(t)
	var next int64 = 7000
	m := &Module{service: s, ids: draftIDSource(func() (int64, error) { next++; return next, nil })}
	s.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }
	ctx := t.Context()
	budgetCap := int64(100)
	draft.project.ProjectID = 10
	draft.project.BudgetCapMicros = &budgetCap
	require.NoError(t, db.Table(s.names.Projects.String()).Create(&draft.project).Error)
	thresholds := []int{}
	s.events = projectPublisher(func(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
		data := e.Data.(map[string]any)
		thresholds = append(thresholds, data["threshold_pct"].(int))
		return 1, tx.Exec("INSERT INTO provision_probe VALUES (?)", e.Type).Error
	})
	for _, amount := range []int64{79, 1, 5, 15, 1} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.spendProjectBudgetOn(ctx, tx, 1, 10, amount) }))
	}
	require.Equal(t, []int{80, 100}, thresholds)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		f, err := m.lockProjectBudgetOn(ctx, tx, 1, 10)
		require.NoError(t, err)
		require.Equal(t, int64(101), f.SpentMicros)
		require.Equal(t, "EXHAUSTED", f.State)
		require.Contains(t, f.Holds, `"BUDGET"`)
		return nil
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.spendProjectBudgetOn(ctx, tx, 1, 10, math.MaxInt64) }), ErrInvalidArgument)
	require.NoError(t, db.Exec("UPDATE "+s.names.Projects.String()+" SET budget_spent_micros=0,budget_state='NORMAL',holds='[]',budget_cap_version=2").Error)
	injected := errors.New("publisher")
	s.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, injected })
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.spendProjectBudgetOn(ctx, tx, 1, 10, 100) }), injected)
	var spent int64
	require.NoError(t, db.Table(s.names.Projects.String()).Select("budget_spent_micros").Scan(&spent).Error)
	require.Zero(t, spent)
}

func TestProjectBudgetThresholdArithmeticRetainsInt64Precision(t *testing.T) {
	s, db, draft := projectHarness(t)
	var next int64 = 7000
	m := &Module{service: s, ids: draftIDSource(func() (int64, error) { next++; return next, nil })}
	s.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }
	ctx := t.Context()
	budgetCap := int64(math.MaxInt64)
	draft.project.ProjectID = 10
	draft.project.BudgetCapMicros = &budgetCap
	draft.project.BudgetNotifyPct = 95
	require.NoError(t, db.Table(s.names.Projects.String()).Create(&draft.project).Error)
	thresholds := []int{}
	s.events = projectPublisher(func(_ context.Context, _ *gorm.DB, e events.Event) (guid.ID, error) {
		thresholds = append(thresholds, e.Data.(map[string]any)["threshold_pct"].(int))
		return 1, nil
	})
	// ceil(MaxInt64*95/100), independently reduced into quotient+remainder.
	threshold := budgetCap/100*95 + (budgetCap%100*95+99)/100
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.spendProjectBudgetOn(ctx, tx, 1, 10, threshold-1) }))
	require.Empty(t, thresholds)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.spendProjectBudgetOn(ctx, tx, 1, 10, 1) }))
	require.Equal(t, []int{95}, thresholds)
}

func TestProjectNotificationAuthorityHonorsOwnershipAndDenials(t *testing.T) {
	service, db, draft := projectHarness(t)
	owner := guid.ID(77)
	draft.project.ProjectID = 10
	draft.project.CreatedBy = &owner
	require.NoError(t, db.Table(service.names.Projects.String()).Create(&draft.project).Error)
	m := &Module{service: service}
	for _, test := range []struct {
		constraint authz.DataConstraint
		want       bool
	}{
		{authz.DataConstraint{AllowAll: true}, true},
		{authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"10"}}, false},
		{authz.DataConstraint{OwnerIDs: []string{"77"}}, true},
		{authz.DataConstraint{OwnerIDs: []string{"88"}}, false},
		{authz.DataConstraint{ResourceIDs: []string{"10"}}, true},
	} {
		m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, principal guid.ID) (authz.DataConstraint, error) {
			require.Equal(t, "project_read", permission)
			return test.constraint, nil
		}
		allowed, err := m.projectNotificationAuthorityOn(t.Context(), db, 1, 10, 11)
		require.NoError(t, err)
		require.Equal(t, test.want, allowed)
	}
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	allowed, err := m.projectNotificationAuthorityOn(t.Context(), db, 2, 10, 11)
	require.NoError(t, err)
	require.False(t, allowed)
	_, err = (*Module)(nil).projectNotificationAuthorityOn(t.Context(), db, 1, 10, 11)
	require.Error(t, err)
}

func TestExecutionAgentReceiptRechecksCurrentAssignmentAuthority(t *testing.T) {
	for _, scenario := range []string{"active", "role_revoked", "inactive", "wrong_agent", "missing", "directory_failure", "member_revoked", "membership_failure"} {
		t.Run(scenario, func(t *testing.T) {
			h, link := executionWorkFixture(t)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				if err := h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE"); err != nil {
					return err
				}
				return h.m.startExecutionWorkOn(h.ctx, tx, link)
			}))
			require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("id=?", link.AssignmentID).Updates(map[string]any{"assignee_kind": "AGENT", "assignee_principal_id": 91, "agent_id": 93}).Error)
			failure := errors.New("current agent facts unavailable")
			h.m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
				require.Equal(t, guid.ID(1), tenant)
				require.Equal(t, guid.ID(91), principal)
				agent := fleet.AgentIdentity{ID: 93, PrincipalID: 91, TenantID: 1, State: "ACTIVE", Roles: []string{"EXECUTOR"}}
				switch scenario {
				case "role_revoked":
					agent.Roles = []string{"REVIEWER"}
				case "inactive":
					agent.State = "REVOKED"
				case "wrong_agent":
					agent.ID++
				case "missing":
					return fleet.AgentIdentity{}, false, nil
				case "directory_failure":
					return fleet.AgentIdentity{}, false, failure
				}
				return agent, true, nil
			}
			h.m.members = memberCheck(func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (bool, error) {
				if principal == 91 {
					if scenario == "membership_failure" {
						return false, failure
					}
					return scenario != "member_revoked", nil
				}
				return tenant == 1 && principal == 11, nil
			})
			before := h.snapshot()
			err := h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.finishExecutionWorkOn(h.ctx, tx, link, ExecutionCompletion{State: "SUCCEEDED", HeadCommit: strings.Repeat("a", 40), Evidence: []ExecutionEvidence{{ID: 8001, Title: "Actual completion head", ContentHash: strings.Repeat("b", 64)}}})
			})
			if scenario == "active" {
				require.NoError(t, err)
				require.Equal(t, link.ExecutionID, *h.stored().CurrentClaimID)
				require.Nil(t, h.stored().CurrentExecutionID)
				return
			}
			if scenario == "directory_failure" || scenario == "membership_failure" {
				require.ErrorIs(t, err, failure)
			} else {
				require.ErrorIs(t, err, ErrAssignmentHolder)
			}
			require.Equal(t, before, h.snapshot(), "rejected current authority cannot submit or clear the execution fence")
		})
	}
}

func TestExecutionResultSuccessionFreezesVerificationAndRollsBack(t *testing.T) {
	for _, scenario := range []string{"COMMAND", "HUMAN", "malformed_plan", "audit_failure"} {
		t.Run(scenario, func(t *testing.T) {
			h, link := executionWorkFixture(t)
			prior, err := h.submit()
			require.NoError(t, err)
			_, err = h.check(prior.ID, 12, "PASSED")
			require.NoError(t, err)
			_, err = h.accept(prior.ID, 13)
			require.NoError(t, err)
			packageView, err := h.handoff(prior.ID)
			require.NoError(t, err)
			_, err = h.receive(prior.ID, packageView.Handoffs[0].ID, 13, false)
			require.NoError(t, err)
			// Acceptance ended the original lease; rework execution requires a
			// distinct active assignment, preserving the historical assignment.
			now, expires := time.Now().UTC(), time.Now().UTC().Add(time.Hour)
			link.AssignmentID = 90000002
			require.NoError(t, query.BindAssignments(h.db, h.m.service.names).WithContext(h.ctx).Create(&model.Assignment{TenantID: 1, ID: link.AssignmentID, ProjectID: 99, WorkItemID: h.work, ActiveSlot: &h.work, Role: "EXECUTOR", AssigneeKind: "HUMAN", AssigneePrincipalID: 11, Status: "ACTIVE", LeaseTTLMS: 3600000, LeaseExpiresAt: &expires, Version: 1, CreatedAt: now, UpdatedAt: now}))
			criterion := *prior.Evidence[0].CriterionID
			method := "COMMAND"
			if scenario == "HUMAN" {
				method = "HUMAN"
			}
			step := workVerificationStep{CriterionID: criterion, Method: method, Detail: "Frozen acceptance method"}
			if method == "COMMAND" {
				step.Argv, step.TimeoutSec = []string{"go", "version"}, 30
			}
			plan, err := json.Marshal([]workVerificationStep{step})
			require.NoError(t, err)
			require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"verification_plan": string(plan), "internal_qa_required": true}).Error)
			approveFixtureReviewPlan(t, h)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				if err := h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE"); err != nil {
					return err
				}
				return h.m.startExecutionWorkOn(h.ctx, tx, link)
			}))
			if scenario == "malformed_plan" {
				// Corruption after launch must roll back completion. A changed plan
				// before launch is already fenced by the immutable approval binding.
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("verification_plan", `[{"method":7}]`).Error)
			}
			before := h.snapshot()
			if scenario == "audit_failure" {
				h.fail = "audit"
			}
			err = h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.finishExecutionWorkOn(h.ctx, tx, link, ExecutionCompletion{State: "SUCCEEDED", HeadCommit: strings.Repeat("a", 40), Evidence: []ExecutionEvidence{{ID: 8001, Title: "Head", ContentHash: strings.Repeat("b", 64)}}})
			})
			if scenario == "malformed_plan" || scenario == "audit_failure" {
				require.Error(t, err)
				require.Equal(t, before, h.snapshot(), "the result, prior pointer, events and execution fence roll back together")
				return
			}
			require.NoError(t, err)
			row, err := h.m.resultOn(h.ctx, h.db, 1, 99, h.work, link.ExecutionID)
			require.NoError(t, err)
			require.Equal(t, prior.Sequence+1, row.Sequence)
			require.Equal(t, prior.ID, *row.PreviousResultID)
			require.Equal(t, string(plan), row.VerificationPlan)
			require.Equal(t, link.ExecutionID, *row.SourceExecutionID)
			want := "INTERNAL_QA"
			if method == "HUMAN" {
				want = "NEEDS_HUMAN_REVIEW"
			}
			require.Equal(t, want, h.stored().State)
			original, err := h.m.resultOn(h.ctx, h.db, 1, 99, h.work, prior.ID)
			require.NoError(t, err)
			require.Equal(t, prior.Sequence, original.Sequence)
			require.Equal(t, prior.Summary, original.Summary)
		})
	}
}

func TestExecutionCompletionStorageFailuresPreserveFencedWorkAndClaim(t *testing.T) {
	for _, stage := range []string{"result_query", "result_create", "execution_clear", "claim_update", "transition_create", "audit"} {
		t.Run(stage, func(t *testing.T) {
			h, link := executionWorkFixture(t)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				if e := h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE"); e != nil {
					return e
				}
				return h.m.startExecutionWorkOn(h.ctx, tx, link)
			}))
			before := h.snapshot()
			failure := errors.New("completion storage refused")
			fired := false
			if stage == "audit" {
				h.m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { fired = true; return failure }
			} else {
				target := h.m.service.names.ResultVersions.String()
				if stage == "execution_clear" || stage == "claim_update" {
					target = h.m.service.names.WorkItems.String()
				}
				if stage == "transition_create" {
					target = h.m.service.names.WorkTransitions.String()
				}
				updates := 0
				hook := func(tx *gorm.DB) {
					if fired || tx.Statement.Table != target {
						return
					}
					if stage == "claim_update" {
						updates++
						if updates < 2 {
							return
						}
					}
					fired = true
					_ = tx.AddError(failure)
				}
				if stage == "result_query" {
					require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("completion_failure", hook))
				} else if stage == "execution_clear" || stage == "claim_update" {
					require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("completion_failure", hook))
				} else {
					require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("completion_failure", hook))
				}
			}
			e := h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.finishExecutionWorkOn(h.ctx, tx, link, ExecutionCompletion{State: "SUCCEEDED", HeadCommit: strings.Repeat("a", 40), Evidence: []ExecutionEvidence{{ID: 8001, Title: "Observed source head", ContentHash: strings.Repeat("b", 64)}}})
			})
			require.True(t, fired)
			require.ErrorIs(t, e, failure)
			_ = h.db.Callback().Query().Remove("completion_failure")
			_ = h.db.Callback().Update().Remove("completion_failure")
			_ = h.db.Callback().Create().Remove("completion_failure")
			require.Equal(t, before, h.snapshot())
			require.Equal(t, link.ExecutionID, *h.stored().CurrentExecutionID)
			require.Nil(t, h.stored().CurrentClaimID)
			var count int64
			require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestExecutionCompletionRejectsInvalidObservedReceiptsWithoutLosingFence(t *testing.T) {
	for _, scenario := range []string{"state", "no_evidence", "head_hex", "zero_id", "duplicate", "empty_title"} {
		t.Run(scenario, func(t *testing.T) {
			h, link := executionWorkFixture(t)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				if e := h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE"); e != nil {
					return e
				}
				return h.m.startExecutionWorkOn(h.ctx, tx, link)
			}))
			input := ExecutionCompletion{State: "SUCCEEDED", HeadCommit: strings.Repeat("a", 40), Evidence: []ExecutionEvidence{{ID: 8001, Title: "Observed head", ContentHash: strings.Repeat("b", 64)}}}
			switch scenario {
			case "state":
				input.State = "UNKNOWN"
			case "no_evidence":
				input.Evidence = nil
			case "head_hex":
				input.HeadCommit = strings.Repeat("z", 40)
			case "zero_id":
				input.Evidence[0].ID = 0
			case "duplicate":
				input.Evidence = append(input.Evidence, input.Evidence[0])
			case "empty_title":
				input.Evidence[0].Title = " "
			}
			before := h.snapshot()
			e := h.db.Transaction(func(tx *gorm.DB) error { return h.m.finishExecutionWorkOn(h.ctx, tx, link, input) })
			require.Error(t, e)
			require.Equal(t, before, h.snapshot())
			require.Equal(t, link.ExecutionID, *h.stored().CurrentExecutionID)
		})
	}
}

func TestExecutionStopAuthorizationFailuresPreserveCurrentFence(t *testing.T) {
	for _, scenario := range []string{"human_failure", "inactive_human", "membership_failure", "inactive_member", "policy_failure", "denied_project", "project_query", "work_query", "assignment_query"} {
		t.Run(scenario, func(t *testing.T) {
			h, link := executionWorkFixture(t)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				if err := h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE"); err != nil {
					return err
				}
				return h.m.startExecutionWorkOn(h.ctx, tx, link)
			}))
			before := h.snapshot()
			h.m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
				require.Equal(t, guid.ID(1), tenant)
				require.Equal(t, guid.ID(99), project)
				require.Equal(t, link.ActorID, actor)
				require.Equal(t, "execution_stop", permission)
				return authz.DataConstraint{AllowAll: true}, nil
			}
			failure := errors.New("current stop authority unavailable")
			expected := failure
			switch scenario {
			case "human_failure", "inactive_human":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					if scenario == "human_failure" {
						return identity.Principal{}, false, failure
					}
					return identity.Principal{}, false, nil
				}
				if scenario == "inactive_human" {
					expected = ErrWorkForbidden
				}
			case "membership_failure", "inactive_member":
				h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
					if scenario == "membership_failure" {
						return false, failure
					}
					return false, nil
				})
				if scenario == "inactive_member" {
					expected = ErrWorkForbidden
				}
			case "policy_failure", "denied_project":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					if scenario == "policy_failure" {
						return authz.DataConstraint{}, failure
					}
					return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}, nil
				}
				if scenario == "denied_project" {
					expected = gorm.ErrRecordNotFound
				}
			}
			injected := false
			if strings.HasSuffix(scenario, "_query") {
				table := h.m.service.names.Projects.String()
				if scenario == "work_query" {
					table = h.m.service.names.WorkItems.String()
				}
				if scenario == "assignment_query" {
					table = h.m.service.names.Assignments.String()
				}
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("stop_authority_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						injected = true
						_ = tx.AddError(failure)
					}
				}))
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				work, assignment, err := h.m.executionStopWorkOn(h.ctx, tx, link, true)
				if err != nil {
					require.Nil(t, assignment)
					if scenario != "assignment_query" {
						require.Nil(t, work)
					}
				}
				return err
			})
			require.ErrorIs(t, err, expected)
			if strings.HasSuffix(scenario, "_query") {
				require.True(t, injected)
				require.NoError(t, h.db.Callback().Query().Remove("stop_authority_failure"))
			}
			require.Equal(t, before, h.snapshot())
		})
	}
}

func TestExecutionEvidenceRejectsForeignCriterionAndUnavailableFilesAtomically(t *testing.T) {
	for _, scenario := range []string{"invalid_coverage", "unavailable_files", "foreign_criterion", "pin_failure", "hash_mismatch", "missing_reference_capability"} {
		t.Run(scenario, func(t *testing.T) {
			h, link := executionWorkFixture(t)
			result, err := h.submit()
			require.NoError(t, err)
			before := h.snapshot()
			artifact := &ExecutionArtifact{SpaceID: 500, FileID: 501, VersionID: 502, FileVersion: 3, Coverage: "COVERED", SHA256: strings.Repeat("a", 64)}
			failure := errors.New("evidence storage unavailable")
			expected := ErrInvalidArgument
			switch scenario {
			case "invalid_coverage":
				artifact.Coverage = "UNPROVEN"
			case "unavailable_files":
				h.m.resultFiles = nil
				expected = ErrUnavailable
			case "foreign_criterion":
				id := guid.ID(999)
				artifact.CriterionID = &id
				expected = gorm.ErrRecordNotFound
			case "pin_failure":
				h.files.fail = failure
				expected = failure
			case "hash_mismatch":
				artifact.SHA256 = strings.Repeat("b", 64)
			case "missing_reference_capability":
				artifact.BindingID = 801
				expected = ErrUnavailable
			}
			err = h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.pinExecutionEvidenceOn(h.ctx, tx, link, result.ID, ExecutionEvidence{Artifact: artifact})
			})
			require.ErrorIs(t, err, expected)
			require.Equal(t, before, h.snapshot())
		})
	}
}

func TestExecutionResultResourceUsesCurrentOrdinaryReadAuthorization(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, h.m.executionWorkAuthorizeResultResource(h.ctx, tx, result.ID, files.ResourceOwner{Scope: "entity", ID: 99}))
		require.ErrorIs(t, h.m.executionWorkAuthorizeResultResource(h.ctx, tx, result.ID, files.ResourceOwner{Scope: "entity", ID: 100}), ErrWorkForbidden)
		require.Error(t, h.m.executionWorkAuthorizeResultResource(h.ctx, tx, 999, files.ResourceOwner{Scope: "entity", ID: 99}))
		return nil
	}))
}

func TestBudgetThresholdHistoryAndAuditFailuresCannotConsumeSpend(t *testing.T) {
	for _, fault := range []string{"id", "zero", "audit", "history", "holds"} {
		t.Run(fault, func(t *testing.T) {
			s, db, draft := projectHarness(t)
			failure := errors.New("budget threshold persistence unavailable")
			next := int64(8000)
			m := &Module{service: s, ids: draftIDSource(func() (int64, error) { next++; return next, nil })}
			s.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 1, nil })
			s.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }
			capacity := int64(100)
			draft.project.ProjectID = 10
			draft.project.BudgetCapMicros = &capacity
			require.NoError(t, db.Table(s.names.Projects.String()).Create(&draft.project).Error)
			want := failure
			switch fault {
			case "id":
				m.ids = unavailableActivityIDs{err: failure}
			case "zero":
				m.ids = draftIDSource(func() (int64, error) { return 0, nil })
				want = ErrUnavailable
			case "audit":
				s.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "history":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("threshold-history-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == s.names.BudgetEvents.String() {
						tx.AddError(failure)
					}
				}))
			case "holds":
				require.NoError(t, db.Table(s.names.Projects.String()).Where("project_id=?", 10).Update("holds", "{}").Error)
				want = ErrUnavailable
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.spendProjectBudgetOn(t.Context(), tx, 1, 10, 100) }), want)
			var row model.Project
			require.NoError(t, db.Table(s.names.Projects.String()).Where("project_id=?", 10).First(&row).Error)
			require.Zero(t, row.BudgetSpentMicros)
			var n int64
			require.NoError(t, db.Table(s.names.BudgetEvents.String()).Count(&n).Error)
			require.Zero(t, n)
		})
	}
}
