package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func planningWorkFixture(t *testing.T) (*resultFixture, ExecutionWorkLink, map[guid.ID]fleet.AgentIdentity) {
	t.Helper()
	h := newResultFixture(t)
	agents := map[guid.ID]fleet.AgentIdentity{
		21: {ID: 121, TenantID: 1, PrincipalID: 21, State: "ACTIVE", Roles: []string{"EXECUTOR"}, ModelID: "executor-model", ModelFamily: "executor-family"},
		22: {ID: 122, TenantID: 1, PrincipalID: 22, State: "ACTIVE", Roles: []string{"REVIEWER"}, ModelID: "reviewer-model", ModelFamily: "reviewer-family"},
		23: {ID: 123, TenantID: 1, PrincipalID: 23, State: "ACTIVE", Roles: []string{"LEAD", "REVIEWER"}, ModelID: "lead-model", ModelFamily: "reviewer-family"},
	}
	h.m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, _ guid.ID, id guid.ID) (fleet.AgentIdentity, bool, error) {
		a, ok := agents[id]
		return a, ok, nil
	}
	h.m.members = memberCheck(func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (bool, error) {
		return tenant == 1 && (id >= 11 && id <= 13 || id >= 21 && id <= 23), nil
	})
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	h.m.deliveryAuthority = func(_ context.Context, _ *gorm.DB, in governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		require.Equal(t, "AGENT", in.ActorKind)
		require.Equal(t, "plan_approve", in.Code)
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "POLICY:plan_review"}, nil
	}
	a := query.BindAssignments(h.db, h.m.service.names)
	assignment, e := a.WithContext(h.ctx).First()
	require.NoError(t, e)
	expires := time.Now().UTC().Add(time.Hour)
	require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("id=?", assignment.ID).Updates(map[string]any{"assignee_kind": "AGENT", "assignee_principal_id": 21, "agent_id": 121, "created_by": 23, "lease_expires_at": expires}).Error)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("plan_required", true).Error)
	require.NoError(t, h.db.Table(h.m.service.names.Repositories.String()).Create(&model.ProjectRepository{TenantID: 1, ID: 400, ProjectID: 99, Name: "primary", RemoteURL: "https://example.test/repository.git", DefaultBranch: "main", ProtectedBranches: "[]", IsPrimary: true, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error)
	h.m.planningRuntime = PlanningRuntime{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{21, 101}, nil }, ContinueOn: func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID, approved bool, _ string) (guid.ID, error) {
		kind := "planning.rework"
		if approved {
			kind = "planning.implementation"
		}
		return 9001, tx.Exec("INSERT INTO provision_probe(step) VALUES (?)", kind).Error
	}}
	return h, ExecutionWorkLink{TenantID: 1, ProjectID: 99, WorkItemID: h.work, AssignmentID: assignment.ID, ExecutionID: 8001, ActorID: 101}, agents
}

func proposeRuntimePlan(t *testing.T, h *resultFixture, l ExecutionWorkLink, risks ...string) ReviewPlanView {
	t.Helper()
	var out ReviewPlanView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		launch, e := h.m.authorizePlanningWorkOn(h.actor(101), tx, l, "CREATE")
		if e != nil {
			return e
		}
		content := ReviewPlanContent{Binding: launch.Binding, Files: []string{"feature.go"}, Risks: append([]string{}, risks...), OutOfScopeConfirmed: true}
		for _, criterion := range launch.Criteria {
			content.CriteriaCoverage = append(content.CriteriaCoverage, PlanCoverage{CriterionID: criterion.ID, Approach: "Implement criterion", Verification: "Run regression"})
		}
		out, e = h.m.completePlanningWorkOn(h.actor(101), tx, l, PlanProducer{ExecutionID: l.ExecutionID, AgentPrincipalID: 21, Model: "executor-model", Family: "executor-family", HeadCommit: strings.Repeat("a", 40)}, content)
		return e
	}))
	return out
}

func TestPlanningRuntimeActualAgentProposalTransitionsWithoutClaim(t *testing.T) {
	h, l, _ := planningWorkFixture(t)
	plan := proposeRuntimePlan(t, h, l)
	require.Equal(t, "AGENT", plan.SourceKind)
	require.EqualValues(t, 21, plan.ProposedBy)
	require.Equal(t, l.ExecutionID, plan.Content.Producer.ExecutionID)
	require.Equal(t, "executor-family", plan.Content.Producer.Family)
	work, e := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, e)
	require.Equal(t, "PLAN_REVIEW", work.State)
	require.Nil(t, work.CurrentExecutionID)
	require.Equal(t, plan.ID, *work.CurrentPlanID)
	require.Nil(t, work.CurrentClaimID)
	var count int64
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.decideReviewPlanOn(h.actor(12), tx, 1, 99, h.work, plan.ID, 12, work.AccountablePrincipalID, plan.Version, plan.ContentHash, "APPROVE", "")
		return e
	}))
	work, e = query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, e)
	require.Equal(t, "CLAIMED", work.State)
	require.NoError(t, h.db.Table("provision_probe").Where("step=?", "planning.implementation").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestPlanningLaunchCurrentFencesRejectStaleOrUnavailableSources(t *testing.T) {
	for _, fault := range []string{"budget", "paused", "baseline", "lease", "human", "ensemble", "agent_missing", "agent", "member", "retired", "policy", "scope", "hidden", "not_required", "execution", "repository", "update"} {
		t.Run(fault, func(t *testing.T) {
			h, l, agents := planningWorkFixture(t)
			injected := errors.New("planning launch unavailable")
			want := ErrAssignmentState
			switch fault {
			case "budget":
				require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id=?", 99).Update("budget_state", "EXHAUSTED").Error)
			case "paused":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("paused_at", time.Now().UTC()).Error)
			case "baseline":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("baseline_stale", true).Error)
			case "lease":
				require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("id=?", l.AssignmentID).Update("lease_expires_at", time.Now().UTC().Add(-time.Hour)).Error)
				want = ErrAssignmentHolder
			case "human":
				require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("id=?", l.AssignmentID).Updates(map[string]any{"assignee_kind": "HUMAN", "agent_id": nil}).Error)
				want = ErrAssignmentHolder
			case "ensemble":
				require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("id=?", l.AssignmentID).Updates(map[string]any{"ensemble_plan_id": 901, "active_slot": nil}).Error)
				want = ErrAssignmentHolder
			case "agent_missing":
				h.m.agents.ByPrincipalOn = nil
				want = ErrUnavailable
			case "agent":
				h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, injected
				}
				want = injected
			case "member":
				h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, injected })
				want = injected
			case "retired":
				a := agents[21]
				a.State = "RETIRED"
				agents[21] = a
				want = ErrAssignmentHolder
			case "policy":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, injected
				}
				want = injected
			case "scope":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
				want = nil
			case "hidden":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
				}
				want = ErrWorkForbidden
			case "not_required":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"plan_required": false, "risk": "LOW", "autonomy_level": "LOW"}).Error)
			case "execution":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("current_execution_id", 900).Error)
			case "repository":
				require.NoError(t, h.db.Table(h.m.service.names.Repositories.String()).Where("project_id=?", 99).Update("is_primary", false).Error)
				want = ErrUnavailable
			case "update":
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("launch-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.service.names.WorkItems.String() {
						tx.AddError(injected)
					}
				}))
				want = injected
			}
			err := h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.authorizePlanningWorkOn(h.actor(101), tx, l, "CREATE"); return e })
			if want == nil {
				require.Error(t, err)
			} else {
				require.ErrorIs(t, err, want)
			}
			row, e := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).Take()
			require.NoError(t, e)
			if fault == "execution" {
				require.Equal(t, guid.ID(900), *row.CurrentExecutionID)
			} else {
				require.Nil(t, row.CurrentExecutionID)
			}
		})
	}
}

func TestPlanningLaunchStorageFailuresLeaveNoExecutionPointer(t *testing.T) {
	h, l, _ := planningWorkFixture(t)
	injected := errors.New("planning storage unavailable")
	reads, failAt := 0, 0
	abort := errors.New("probe rollback")
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("planning-read-failure", func(tx *gorm.DB) {
		reads++
		if reads == failAt {
			tx.AddError(injected)
		}
	}))
	defer h.db.Callback().Query().Remove("planning-read-failure")
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.authorizePlanningWorkOn(h.actor(101), tx, l, "CREATE")
		if e != nil {
			return e
		}
		return abort
	}), abort)
	total := reads
	require.Greater(t, total, 4)
	for i := 1; i <= total; i++ {
		reads, failAt = 0, i
		err := h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.authorizePlanningWorkOn(h.actor(101), tx, l, "CREATE"); return e })
		require.Error(t, err, "source read %d must fail closed", i)
	}
	row, e := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).Take()
	require.NoError(t, e)
	require.Nil(t, row.CurrentExecutionID)
}

func TestPlanningCompletionRollsBackEveryFailedPersistenceStage(t *testing.T) {
	for _, fault := range []string{"producer", "coverage", "binding", "read", "supersede", "id", "create", "pointer", "audit", "event"} {
		t.Run(fault, func(t *testing.T) {
			h, l, _ := planningWorkFixture(t)
			injected := errors.New("plan completion unavailable")
			want := injected
			e := h.db.Transaction(func(tx *gorm.DB) error {
				launch, e := h.m.authorizePlanningWorkOn(h.actor(101), tx, l, "CREATE")
				if e != nil {
					return e
				}
				content := ReviewPlanContent{Binding: launch.Binding, Files: []string{"feature.go"}, OutOfScopeConfirmed: true}
				for _, c := range launch.Criteria {
					content.CriteriaCoverage = append(content.CriteriaCoverage, PlanCoverage{CriterionID: c.ID, Approach: "Implement criterion", Verification: "Run regression"})
				}
				producer := PlanProducer{ExecutionID: l.ExecutionID, AgentPrincipalID: 21, Model: "executor-model", Family: "executor-family", HeadCommit: strings.Repeat("a", 40)}
				switch fault {
				case "producer":
					producer.ExecutionID++
					want = ErrInvalidArgument
				case "coverage":
					content.CriteriaCoverage = nil
					want = nil
				case "binding":
					content.Binding = nil
					want = nil
				case "id":
					h.m.ids = unavailableActivityIDs{err: injected}
				case "read":
					require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("completion-failure", func(tx *gorm.DB) {
						if tx.Statement.Table == h.m.service.names.ReviewPlans.String() {
							tx.AddError(injected)
						}
					}))
					defer h.db.Callback().Query().Remove("completion-failure")
				case "supersede", "pointer":
					table := h.m.service.names.ReviewPlans.String()
					if fault == "pointer" {
						table = h.m.service.names.WorkItems.String()
					}
					require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("completion-failure", func(tx *gorm.DB) {
						if tx.Statement.Table == table {
							tx.AddError(injected)
						}
					}))
					defer h.db.Callback().Update().Remove("completion-failure")
				case "create":
					require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("completion-failure", func(tx *gorm.DB) {
						if tx.Statement.Table == h.m.service.names.ReviewPlans.String() {
							tx.AddError(injected)
						}
					}))
					defer h.db.Callback().Create().Remove("completion-failure")
				case "audit":
					h.m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return injected }
				case "event":
					h.m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, injected })
				}
				_, e = h.m.completePlanningWorkOn(h.actor(101), tx, l, producer, content)
				return e
			})
			if want == nil {
				require.Error(t, e)
			} else {
				require.ErrorIs(t, e, want)
			}
			row, e := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).Take()
			require.NoError(t, e)
			require.Nil(t, row.CurrentExecutionID)
			require.Nil(t, row.CurrentPlanID)
			count, e := query.BindReviewPlans(h.db, h.m.service.names).WithContext(h.ctx).Count()
			require.NoError(t, e)
			require.Zero(t, count)
		})
	}
}

func TestPlanningReviewCompletionUsesCurrentSourceAndAtomicDecision(t *testing.T) {
	for _, fault := range []string{"ok", "paused", "member", "read", "agent", "authority", "binding"} {
		t.Run(fault, func(t *testing.T) {
			h, l, _ := planningWorkFixture(t)
			plan := proposeRuntimePlan(t, h, l)
			injected := errors.New("review source unavailable")
			want := error(nil)
			switch fault {
			case "paused":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("paused_at", time.Now().UTC()).Error)
				want = ErrAssignmentState
			case "member":
				h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, injected })
				want = injected
			case "read":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("review-read-failure", func(tx *gorm.DB) { tx.AddError(injected) }))
				want = injected
			case "agent":
				h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, injected
				}
				want = injected
			case "authority":
				h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{}, injected
				}
				want = injected
			case "binding":
				require.NoError(t, h.db.Table(h.m.service.names.WorkCriteria.String()).Where("work_item_id=?", h.work).Update("text", "Changed current criterion").Error)
				want = ErrPlanNotApproved
			}
			e := h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.completePlanningReviewOn(h.actor(22), tx, 1, plan.ID, 22, plan.Version, plan.ContentHash, "APPROVE", "")
			})
			require.ErrorIs(t, e, want)
			if fault == "read" {
				require.NoError(t, h.db.Callback().Query().Remove("review-read-failure"))
			}
			stored, e := query.BindReviewPlans(h.db, h.m.service.names).WithContext(h.ctx).Take()
			require.NoError(t, e)
			if fault == "ok" {
				require.Equal(t, "APPROVED", stored.State)
			} else {
				require.Equal(t, "PROPOSED", stored.State)
			}
		})
	}
}

func TestPlanningRuntimeAgentReviewRequiresCurrentRoleFamilyPolicyAndDispatchIndependence(t *testing.T) {
	for _, kind := range []string{"same-family", "missing-role", "dispatcher", "policy-denied", "valid"} {
		t.Run(kind, func(t *testing.T) {
			h, l, agents := planningWorkFixture(t)
			plan := proposeRuntimePlan(t, h, l)
			actor := guid.ID(22)
			switch kind {
			case "same-family":
				a := agents[22]
				a.ModelFamily = "executor-family"
				agents[22] = a
			case "missing-role":
				a := agents[22]
				a.Roles = []string{"EXECUTOR"}
				agents[22] = a
			case "dispatcher":
				actor = 23
			case "policy-denied":
				h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:reviewer"}, nil
				}
			}
			e := h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.decideReviewPlanOn(h.actor(actor), tx, 1, 99, h.work, plan.ID, actor, 11, plan.Version, plan.ContentHash, "APPROVE", "")
				return e
			})
			if kind == "valid" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
				stored, e := query.BindReviewPlans(h.db, h.m.service.names).WithContext(h.ctx).First()
				require.NoError(t, e)
				require.Equal(t, "PROPOSED", stored.State)
			}
		})
	}
}

func TestPlanningRuntimeContinuationFailureRollsBackApprovalAndWorkTransition(t *testing.T) {
	h, l, _ := planningWorkFixture(t)
	plan := proposeRuntimePlan(t, h, l)
	failure := errors.New("machine unavailable")
	h.m.planningRuntime.ContinueOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool, string) (guid.ID, error) {
		return 0, failure
	}
	e := h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.decideReviewPlanOn(h.actor(12), tx, 1, 99, h.work, plan.ID, 12, 11, plan.Version, plan.ContentHash, "APPROVE", "")
		return e
	})
	require.ErrorIs(t, e, failure)
	stored, e := query.BindReviewPlans(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, e)
	require.Equal(t, "PROPOSED", stored.State)
	require.Nil(t, stored.DecidedBy)
	work, e := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, e)
	require.Equal(t, "PLAN_REVIEW", work.State)
}

func TestPlanningRuntimeCanonicalDispatchAutomaticallyLaunchesOrRollsBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "launch", true: "rollback"}[fail], func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 112, TenantID: tenant, PrincipalID: id, State: "ACTIVE", Roles: []string{"EXECUTOR"}}, true, nil
			}
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("plan_required", true).Error)
			calls := 0
			failure := errors.New("planning runtime unavailable")
			m.planningRuntime.LaunchOn = func(_ context.Context, tx *gorm.DB, in PlanningLaunch) (guid.ID, error) {
				calls++
				require.Equal(t, work.ID, in.WorkItemID)
				require.Positive(t, int64(in.AssignmentID))
				require.EqualValues(t, 12, in.AgentPrincipalID)
				if fail {
					return 0, failure
				}
				return 8001, tx.Exec("INSERT INTO provision_probe(step) VALUES ('planning.created')").Error
			}
			e := db.Transaction(func(tx *gorm.DB) error {
				_, e := (clientWork{m: m}).dispatchApplyOn(ctx, tx, 1, 99, DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 9000, AssigneePrincipalID: 12, ExpectedWorkVersion: work.Version, Reason: "Reviewed candidate"})
				return e
			})
			require.Equal(t, 1, calls)
			var count int64
			require.NoError(t, db.Table(m.service.names.Assignments.String()).Count(&count).Error)
			if fail {
				require.ErrorIs(t, e, failure)
				require.Zero(t, count)
			} else {
				require.NoError(t, e)
				require.EqualValues(t, 1, count)
			}
		})
	}
}

func TestPlanningRuntimeRoutesEligibleReviewerAndEscalatesThirdRejection(t *testing.T) {
	h, l, agents := planningWorkFixture(t)
	h.m.agents.ActiveByRoleOn = func(context.Context, *gorm.DB, guid.ID, string) ([]fleet.AgentIdentity, error) {
		return []fleet.AgentIdentity{agents[23], agents[22], agents[21]}, nil
	}
	reviews := 0
	h.m.planningRuntime.ReviewOn = func(_ context.Context, tx *gorm.DB, _, _, reviewer guid.ID) (guid.ID, error) {
		require.EqualValues(t, 22, reviewer)
		reviews++
		return 9100, tx.Exec("INSERT INTO provision_probe(step) VALUES ('planning.review')").Error
	}
	for round := 1; round <= 3; round++ {
		l.ExecutionID = guid.ID(8000 + round)
		plan := proposeRuntimePlan(t, h, l)
		require.Equal(t, round, plan.Round)
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.decideReviewPlanOn(h.actor(22), tx, 1, 99, h.work, plan.ID, 22, 11, plan.Version, plan.ContentHash, "REJECT", "Add exact criterion verification")
			return e
		}))
		work, e := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).First()
		require.NoError(t, e)
		if round < 3 {
			require.Equal(t, "CLAIMED", work.State)
		} else {
			require.Equal(t, "NEEDS_HUMAN_REVIEW", work.State)
			require.True(t, work.RequireHumanDecision)
			require.Equal(t, "INTERNAL_ROUNDS_EXHAUSTED", *work.EscalationReason)
		}
	}
	require.Equal(t, 3, reviews)
	var n int64
	require.NoError(t, h.db.Table("provision_probe").Where("step=?", "planning.rework").Count(&n).Error)
	require.EqualValues(t, 2, n)
}

func TestPlanningRuntimeApprovedPromptBindsPlanAndRejectsTampering(t *testing.T) {
	h, l, _ := planningWorkFixture(t)
	risks := make([]string, 12)
	for i := range risks {
		risks[i] = strings.Repeat("r", 2000)
	}
	plan := proposeRuntimePlan(t, h, l, risks...)
	frozen, e := json.Marshal(plan.Content)
	require.NoError(t, e)
	require.Greater(t, len(frozen), 20000)
	require.Less(t, len(frozen), 65536)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.decideReviewPlanOn(h.actor(12), tx, 1, 99, h.work, plan.ID, 12, 11, plan.Version, plan.ContentHash, "APPROVE", "")
		return e
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		producer, e := h.m.planningImplementationOn(h.ctx, tx, 1, plan.ID, l.ExecutionID)
		if e == nil {
			require.Equal(t, plan.Content.Producer.Objective, producer.Objective)
			require.Equal(t, plan.Content, *producer.ApprovedPlan)
			raw, marshalErr := json.Marshal(producer)
			require.NoError(t, marshalErr)
			require.NotContains(t, string(raw), "approved_plan", "private transport cannot recurse into the persisted producer")
		}
		return e
	}))
	require.NoError(t, h.db.Table(h.m.service.names.ReviewPlans.String()).Where("id=?", plan.ID).Update("content", "{}").Error)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.planningImplementationOn(h.ctx, tx, 1, plan.ID, l.ExecutionID)
		return e
	}))
}

func TestPlanningRuntimeApprovedSystemImplementationUsesRealDeliveryExecutionGate(t *testing.T) {
	h, l, _ := planningWorkFixture(t)
	plan := proposeRuntimePlan(t, h, l)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.decideReviewPlanOn(h.actor(12), tx, 1, 99, h.work, plan.ID, 12, 11, plan.Version, plan.ContentHash, "APPROVE", "")
		return e
	}))
	planned := ExecutionWorkLink{TenantID: 1, ProjectID: 99, WorkItemID: h.work, AssignmentID: l.AssignmentID, ExecutionID: 9001, ParentExecutionID: l.ExecutionID, SourceDecisionID: plan.ID, ReviewPlanID: plan.ID}
	system := authn.WithClaims(h.ctx, &authn.Claims{TenantID: 1})
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(system, tx, planned, "CREATE") }))
	work, e := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, e)
	require.Equal(t, planned.ExecutionID, *work.CurrentExecutionID)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.startExecutionWorkOn(system, tx, planned) }))
	work, e = query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, e)
	require.Equal(t, "IN_PROGRESS", work.State)
	require.NoError(t, h.db.Table(h.m.service.names.ReviewPlans.String()).Where("id=?", plan.ID).Update("state", "REJECTED").Error)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(system, tx, planned, "DISPATCH") }))
}

func TestPlanningRuntimeStopRemainsAvailableAfterExecutorRevocation(t *testing.T) {
	h, l, _ := planningWorkFixture(t)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.authorizePlanningWorkOn(h.actor(101), tx, l, "CREATE"); return e }))
	h.m.agents.ByPrincipalOn = nil
	h.m.members = nil
	require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("id=?", l.AssignmentID).Updates(map[string]any{"status": "EXPIRED", "active_slot": nil, "ended_at": time.Now().UTC(), "end_reason": "LEASE_EXPIRED"}).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { l.ActorID = 12; return h.m.endPlanningWorkOn(h.actor(12), tx, l, "CANCELLED") })) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	work, e := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, e)
	require.Nil(t, work.CurrentExecutionID)
	require.Equal(t, "CLAIMED", work.State)
	require.Nil(t, work.CurrentClaimID)
}

func TestPlanningPrincipalHintsUseCurrentWorkAndRegisteredRuntime(t *testing.T) {
	h, l, _ := planningWorkFixture(t)
	services := module.NewServices()
	require.NoError(t, h.m.providePlanningRuntime(services))
	require.NoError(t, services.Seal())
	owner, e := module.Resolve[PlanningWork](services)
	require.NoError(t, e)
	require.ErrorIs(t, owner.Bind(PlanningRuntime{}), ErrInvalidArgument)
	runtime := h.m.planningRuntime
	runtime.LaunchOn = func(context.Context, *gorm.DB, PlanningLaunch) (guid.ID, error) { return 900, nil }
	require.NoError(t, owner.Bind(runtime))
	require.ErrorIs(t, owner.Bind(runtime), ErrInvalidArgument)
	calls, failAt := 0, 0
	failure := errors.New("current planning principal source unavailable")
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("planning-principal-source", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	defer h.db.Callback().Query().Remove("planning-principal-source")
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			ids, e := owner.PrincipalsOn(h.actor(101), tx, l)
			if e == nil {
				require.Contains(t, ids, guid.ID(11))
				require.Contains(t, ids, guid.ID(21))
			}
			return e
		})
	}
	require.NoError(t, read())
	total := calls
	require.Greater(t, total, 1)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, read(), failure)
	}
}

func TestPlanningRuntimePersistenceFailuresPreserveProposalReviewAndExecution(t *testing.T) {
	for _, operation := range []string{"proposal", "review", "end"} {
		t.Run(operation, func(t *testing.T) {
			h, l, _ := planningWorkFixture(t)
			var plan ReviewPlanView
			if operation == "review" {
				plan = proposeRuntimePlan(t, h, l)
			}
			if operation == "end" {
				require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.authorizePlanningWorkOn(h.actor(101), tx, l, "CREATE"); return e }))
			}
			before := h.snapshot()
			rollback := errors.New("rollback successful planning mutation")
			failure := errors.New("current planning storage unavailable")
			calls, failAt := 0, 0
			probe := func(tx *gorm.DB) {
				calls++
				if calls == failAt {
					tx.AddError(failure)
				}
			}
			require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("planning-mutation-storage", probe))
			require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("planning-mutation-storage", probe))
			require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("planning-mutation-storage", probe))
			run := func() error {
				return h.db.Transaction(func(tx *gorm.DB) error {
					var e error
					switch operation {
					case "proposal":
						launch, x := h.m.authorizePlanningWorkOn(h.actor(101), tx, l, "CREATE")
						if x != nil {
							return x
						}
						content := ReviewPlanContent{Binding: launch.Binding, Files: []string{"feature.go"}, OutOfScopeConfirmed: true}
						for _, c := range launch.Criteria {
							content.CriteriaCoverage = append(content.CriteriaCoverage, PlanCoverage{CriterionID: c.ID, Approach: "Implement current criterion", Verification: "Run regression"})
						}
						_, e = h.m.completePlanningWorkOn(h.actor(101), tx, l, PlanProducer{ExecutionID: l.ExecutionID, AgentPrincipalID: 21, Model: "executor-model", Family: "executor-family", HeadCommit: strings.Repeat("a", 40)}, content)
					case "review":
						e = h.m.completePlanningReviewOn(h.actor(22), tx, 1, plan.ID, 22, plan.Version, plan.ContentHash, "APPROVE", "")
					case "end":
						l.ActorID = 12
						e = h.m.endPlanningWorkOn(h.actor(12), tx, l, "CANCELLED") //nolint:misspell // Preserve the published domain or serialized contract spelling.
					}
					if e != nil {
						return e
					}
					return rollback
				})
			}
			require.ErrorIs(t, run(), rollback)
			total := calls
			require.Greater(t, total, 3)
			for i := 1; i <= total; i++ {
				calls, failAt = 0, i
				e := run()
				require.Error(t, e, "operation %s stage %d", operation, i)
				require.NotErrorIs(t, e, rollback, "failed storage must not finish %s stage %d", operation, i)
			}
			require.NoError(t, h.db.Callback().Query().Remove("planning-mutation-storage"))
			require.NoError(t, h.db.Callback().Create().Remove("planning-mutation-storage"))
			require.NoError(t, h.db.Callback().Update().Remove("planning-mutation-storage"))
			require.Equal(t, before, h.snapshot())
		})
	}
}
