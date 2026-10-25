package execution

import (
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func planningExecutionFixture(t *testing.T) (*executionFixture, delivery.PlanningLaunch) {
	t.Helper()
	f := executionHarness(t)
	f.m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{TenantID: tenant, PrincipalID: id, State: "ACTIVE", Roles: []string{"EXECUTOR", "REVIEWER"}, RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "family", AuthorizationMode: "WORKSPACE_WRITE"}, true, nil
	}
	in := delivery.PlanningLaunch{TenantID: 7, ProjectID: 9, WorkItemID: 3, AssignmentID: 6, AgentPrincipalID: 21, RepositoryID: 4, Objective: "Implement actual requirement", DefaultBranch: "main", Binding: &delivery.ReviewPlanBinding{}, Criteria: []delivery.PlanningCriterion{{ID: 41, Text: "Acceptance", Method: "test"}}}
	f.m.planning = delivery.PlanningWork{AuthorizeOn: func(_ context.Context, _ *gorm.DB, l delivery.ExecutionWorkLink, _ string) (delivery.PlanningLaunch, error) {
		if !f.authorized {
			return delivery.PlanningLaunch{}, ErrForbidden
		}
		require.Equal(t, in.WorkItemID, l.WorkItemID)
		return in, nil
	}, CompleteOn: func(_ context.Context, tx *gorm.DB, l delivery.ExecutionWorkLink, p delivery.PlanProducer, _ delivery.ReviewPlanContent) (delivery.ReviewPlanView, error) {
		require.Equal(t, l.ExecutionID, p.ExecutionID)
		require.EqualValues(t, 21, p.AgentPrincipalID)
		require.Equal(t, "observed-model", p.Model)
		return delivery.ReviewPlanView{}, tx.Exec("INSERT INTO effects(kind) VALUES ('planning.proposed')").Error
	}, EndOn: func(_ context.Context, tx *gorm.DB, _ delivery.ExecutionWorkLink, state string) error {
		return tx.Exec("INSERT INTO effects(kind) VALUES (?)", "planning."+state).Error
	}}
	f.m.work.SettleBudgetOn = func(_ context.Context, tx *gorm.DB, _ WorkLink, _ WorkResult) error {
		return tx.Exec("INSERT INTO effects(kind) VALUES ('planning.settled')").Error
	}
	return f, in
}

func launchPlanningTest(t *testing.T, f *executionFixture, in delivery.PlanningLaunch) guid.ID {
	t.Helper()
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.launchPlanningOn(f.human, tx, in); return e }))
	return id
}

func TestPlanningPersistedLaunchFencedReceiptReplayAndRollback(t *testing.T) {
	f, in := planningExecutionFixture(t)
	id := launchPlanningTest(t, f, in)
	v := f.pull(t, id)
	require.NotNil(t, v.Plan.Planning)
	require.Equal(t, "READ_ONLY", v.Plan.AuthorizationMode)
	require.Equal(t, "CREATED", v.State)
	_, e := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, e)
	terminal := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", HeadCommit: strings.Repeat("a", 40), Content: `{"files":["feature.go"]}`}
	f.pub.fail = true
	_, e = f.receipt(id, terminal)
	require.Error(t, e)
	require.Equal(t, "RUNNING", f.read(t, id).State)
	var n int64
	require.NoError(t, f.db.Table("effects").Where("kind='planning.proposed'").Count(&n).Error)
	require.Zero(t, n)
	f.pub.fail = false
	r, e := f.receipt(id, terminal)
	require.NoError(t, e)
	require.Equal(t, "SUCCEEDED", r.State)
	r, e = f.receipt(id, terminal)
	require.NoError(t, e)
	require.True(t, r.Replayed)
	terminal.Content = `{}`
	_, e = f.receipt(id, terminal)
	require.ErrorIs(t, e, ErrConflict)
	f.authorized = false
	terminal.Content = `{"files":["feature.go"]}`
	_, e = f.receipt(id, terminal)
	require.ErrorIs(t, e, ErrForbidden)
	require.NoError(t, f.db.Table("effects").Where("kind='planning.proposed'").Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Count(&n).Error)
	require.Zero(t, n)
}

func TestPlanningCancelPauseUnknownAndExplicitRecovery(t *testing.T) {
	for _, kind := range []string{"unleased", "leased", "unknown", "pause"} {
		t.Run(kind, func(t *testing.T) {
			f, in := planningExecutionFixture(t)
			id := launchPlanningTest(t, f, in)
			if kind == "unleased" {
				f.m.agentRuntime.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
					return fleet.AgentRuntimeFact{}, false, nil
				}
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CancelOn(f.human, tx, 7, id, 1); return e }))
				require.Equal(t, StateCanceled, f.read(t, id).State)
				return
			}
			f.pull(t, id)
			_, e := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, e)
			switch kind {
			case "leased":
				v := f.read(t, id)
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CancelOn(f.human, tx, 7, id, v.Version); return e }))
				r, e := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "CANCELLED"}) //nolint:misspell // Preserve the published domain or serialized contract spelling.
				require.NoError(t, e)
				require.Equal(t, StateCanceled, r.State)
			case "unknown":
				r, e := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "LOST"})
				require.NoError(t, e)
				require.Equal(t, "INTERRUPTED", r.State)
				v := f.read(t, id)
				require.Equal(t, "UNKNOWN", v.ResultState)
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.RecoverOn(f.human, tx, 7, id, v.Version, true); return e }))
				require.EqualValues(t, 2, f.read(t, id).DispatchEpoch)
			case "pause":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Update("state_reason", "PAUSE_REQUESTED").Error)
				r, e := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "PAUSED"})
				require.NoError(t, e)
				require.Equal(t, "PAUSED", r.State)
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Update("state_reason", "RESUME_REQUESTED").Error)
				r, e = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "RESUMED"})
				require.NoError(t, e)
				require.Equal(t, "RUNNING", r.State)
			}
		})
	}
}

func TestPlanningLaunchFailureIsAtomicAndManualPurposeIgnored(t *testing.T) {
	f, in := planningExecutionFixture(t)
	f.pub.fail = true
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.launchPlanningOn(f.human, tx, in); return e }))
	var n int64
	require.NoError(t, f.db.Table("slots").Count(&n).Error)
	require.Zero(t, n)
	f.pub.fail = false
	c := validCreate()
	c.Plan.Planning = &PlanningPlan{}
	c.Plan.ApprovedPlanID = 999
	c.Plan.ApprovedPlan = &delivery.ReviewPlanContent{Files: []string{"injected.go"}}
	var v ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; v, e = f.m.CreateOn(f.human, tx, 7, c); return e }))
	require.Nil(t, v.Plan.Planning)
	require.Zero(t, v.Plan.ApprovedPlanID)
	require.Nil(t, v.Plan.ApprovedPlan)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.launchPlanningOn(f.human, tx, in); return e }), ErrConflict)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

func TestPlanningIndependentReviewerRuntimeAndApprovedImplementationContinuation(t *testing.T) {
	f, in := planningExecutionFixture(t)
	sourceID := launchPlanningTest(t, f, in)
	f.pull(t, sourceID)
	_, e := f.receipt(sourceID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, e)
	_, e = f.receipt(sourceID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", HeadCommit: strings.Repeat("a", 40), Content: `{"files":["feature.go"]}`})
	require.NoError(t, e)
	producer := delivery.PlanProducer{ExecutionID: sourceID, AgentPrincipalID: 21, Model: "observed-model", Family: "family", HeadCommit: strings.Repeat("a", 40), Objective: "Implement the approved frozen execution plan exactly"}
	plan := delivery.ReviewPlanView{Content: delivery.ReviewPlanContent{Producer: &producer, Binding: in.Binding, Files: []string{"feature.go"}}}
	for range 12 {
		plan.Content.Risks = append(plan.Content.Risks, strings.Repeat("r", 2000))
	}
	frozen, e := json.Marshal(plan.Content)
	require.NoError(t, e)
	require.Greater(t, len(frozen), 20000)
	require.Less(t, len(frozen), 65536)
	plan.ID = 81
	plan.ProjectID = 9
	plan.WorkItemID = 3
	plan.Version = 1
	plan.ContentHash = strings.Repeat("b", 64)
	decided := false
	var implementationID guid.ID
	f.m.planning.AuthorizeReviewOn = func(_ context.Context, _ *gorm.DB, tenant, id, reviewer guid.ID, replay bool) (delivery.ReviewPlanView, error) {
		require.EqualValues(t, 22, reviewer)
		require.EqualValues(t, 81, id)
		if decided && !replay {
			return delivery.ReviewPlanView{}, delivery.ErrPlanNotApproved
		}
		return plan, nil
	}
	f.m.planning.ImplementationOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanProducer, error) {
		approved := producer
		approved.ApprovedPlan = &plan.Content
		return approved, nil
	}
	f.m.planning.CompleteReviewOn = func(ctx context.Context, tx *gorm.DB, tenant, id, reviewer guid.ID, version int, digest, action, feedback string) error {
		require.Equal(t, "APPROVE", action)
		require.Equal(t, plan.ContentHash, digest)
		var e error
		implementationID, e = f.m.continuePlanningOn(ctx, tx, tenant, id, sourceID, true, feedback)
		if e == nil {
			decided = true
		}
		return e
	}
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
	original := f.m.agents.ByPrincipalOn
	f.m.agents.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		a, ok, e := original(ctx, tx, tenant, id)
		if id == 22 {
			a.ModelID = "reviewer-model"
			a.ModelFamily = "reviewer-family"
		}
		return a, ok, e
	}
	var reviewID guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		reviewID, e = f.m.launchPlanningReviewOn(f.human, tx, 7, 81, 22)
		return e
	}))
	v := f.pull(t, reviewID)
	require.Equal(t, "reviewer-family", v.Plan.ModelFamily)
	require.NotNil(t, v.Plan.Planning.Review)
	require.Equal(t, plan.Content, *v.Plan.Planning.Review.Content)
	require.Equal(t, in.Objective, v.Plan.Objective)
	require.Equal(t, "READ_ONLY", v.Plan.AuthorizationMode)
	_, e = f.receipt(reviewID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, e)
	terminal := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", HeadCommit: producer.HeadCommit, Content: `{"action":"APPROVE","feedback":"Criteria covered"}`}
	_, e = f.receipt(reviewID, terminal)
	require.NoError(t, e)
	require.Positive(t, int64(implementationID))
	implementation := f.read(t, implementationID)
	require.Nil(t, implementation.Plan.Planning)
	require.EqualValues(t, 81, implementation.Plan.ApprovedPlanID)
	require.Equal(t, plan.Content, *implementation.Plan.ApprovedPlan)
	require.Equal(t, "WORKSPACE_WRITE", implementation.Plan.AuthorizationMode)
	require.Equal(t, producer.Objective, implementation.Plan.Objective)
	require.Equal(t, producer.HeadCommit, implementation.Plan.BaseCommit)
	replay, e := f.receipt(reviewID, terminal)
	require.NoError(t, e)
	require.True(t, replay.Replayed)
}

func TestPlanningLargeFrozenCriteriaKeepBoundedOriginalObjective(t *testing.T) {
	f, in := planningExecutionFixture(t)
	authorize := f.m.planning.AuthorizeOn
	criteria := make([]delivery.PlanningCriterion, 12)
	for i := range criteria {
		criteria[i] = delivery.PlanningCriterion{ID: guid.ID(i + 41), Text: strings.Repeat("c", 2000), Method: "test"}
	}
	original := strings.Repeat("o", 20000)
	f.m.planning.AuthorizeOn = func(ctx context.Context, tx *gorm.DB, link delivery.ExecutionWorkLink, op string) (delivery.PlanningLaunch, error) {
		facts, err := authorize(ctx, tx, link, op)
		facts.Criteria, facts.Objective = criteria, original
		return facts, err
	}
	in.Objective = "Previous review rejected the plan. Address these findings: cover the edge case."
	id := launchPlanningTest(t, f, in)
	v := f.pull(t, id)
	require.Equal(t, original, v.Plan.Objective)
	require.Equal(t, criteria, v.Plan.Planning.Criteria)
	require.Equal(t, in.Objective, v.Plan.Planning.Feedback)
	raw, err := json.Marshal(v.Plan.Planning)
	require.NoError(t, err)
	require.Greater(t, len(raw), 20000)
}

func TestPlanningUnleasedSupersededReviewerIsCancelledWithoutExecution(t *testing.T) {
	f, in := planningExecutionFixture(t)
	id := launchPlanningTest(t, f, in)
	v := f.read(t, id)
	v.Plan.Planning.Review = &PlanningReview{PlanID: 81, Version: 1, ContentHash: "frozen"}
	raw, e := json.Marshal(v.Plan)
	require.NoError(t, e)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Updates(map[string]any{"branch_reason": "PLAN_REVIEW", "plan_json": string(raw), "plan_hash": hash(raw)}).Error)
	f.m.planning.AuthorizeReviewOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (delivery.ReviewPlanView, error) {
		return delivery.ReviewPlanView{}, delivery.ErrPlanNotApproved
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		rows, e := f.m.PullOn(f.runner, tx, 7, f.fence)
		require.Empty(t, rows)
		return e
	}))
	require.Equal(t, StateCanceled, f.read(t, id).State)
}

func TestPlanningEveryStorageFailurePreservesDispatchAndSourceEffects(t *testing.T) {
	for _, command := range []string{"launch", "pull", "accepted", "complete", "cancel", "recover"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f, in := planningExecutionFixture(t)
				var id guid.ID
				if command != "launch" {
					id = launchPlanningTest(t, f, in)
				}
				if command == "accepted" || command == "complete" || command == "recover" {
					f.pull(t, id)
				}
				if command == "complete" {
					_, e := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
					require.NoError(t, e)
				}
				if command == "recover" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.ExpireOn(f.human, tx, 7, id, time.Now().Add(time.Hour)) }))
				}
				version := 1
				if id > 0 {
					version = f.read(t, id).Version
				}
				snapshot := func() map[string][]map[string]any {
					out := map[string][]map[string]any{}
					for _, table := range []string{f.m.names.Executions.String(), f.m.names.Receipts.String(), f.m.names.Logs.String(), f.m.names.Evidence.String(), f.m.names.Approvals.String(), "slots", "effects"} {
						var rows []map[string]any
						require.NoError(t, f.db.Table(table).Order("rowid").Find(&rows).Error)
						out[table] = rows
					}
					return out
				}
				before := snapshot()
				count := 0
				injected := errors.New("execution transactional storage unavailable")
				hook := func(tx *gorm.DB) {
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("execution_transaction_failure", hook))
				case "create":
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("execution_transaction_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("execution_transaction_failure", hook))
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					switch command {
					case "launch":
						_, e := f.m.launchPlanningOn(f.human, tx, in)
						return e
					case "pull":
						_, e := f.m.PullOn(f.runner, tx, 7, f.fence)
						return e
					case "accepted":
						_, e := f.m.ReceiptOn(f.runner, tx, 7, id, f.fence, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
						return e
					case "complete":
						_, e := f.m.ReceiptOn(f.runner, tx, 7, id, f.fence, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", HeadCommit: strings.Repeat("a", 40), Content: `{"files":["feature.go"]}`})
						return e
					case "recover":
						_, e := f.m.RecoverOn(f.human, tx, 7, id, version, true)
						return e
					default:
						_, e := f.m.CancelOn(f.human, tx, 7, id, version)
						return e
					}
				})
				require.NoError(t, f.db.Callback().Query().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Create().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("execution_transaction_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				require.Equal(t, before, snapshot(), "failed command must roll back execution, journals, slots and effects")
				return count
			}
			total := 0
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { total = exercise(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/failure_%d", command, kind, at), func(t *testing.T) { require.GreaterOrEqual(t, exercise(t, at), at) })
			}
		}
	}
}

func TestPlanningCurrentAgentAndRuntimeFailuresNeverReserveExecution(t *testing.T) {
	for _, fault := range []string{"agent_source", "agent_missing", "agent_retired", "agent_role", "agent_family", "runtime_source", "runtime_missing", "runtime_machine", "runtime_offline", "runtime_kind", "id_error", "id_zero", "facts_source", "facts_owner", "workspace_source", "objective_bounds"} {
		t.Run(fault, func(t *testing.T) {
			f, in := planningExecutionFixture(t)
			injected := errors.New("planning current source unavailable")
			agentSource := f.m.agents.ByPrincipalOn
			runtimeSource := f.m.agentRuntime.ByPrincipalOn
			switch fault {
			case "agent_source", "agent_missing", "agent_retired", "agent_role", "agent_family":
				f.m.agents.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
					a, found, e := agentSource(ctx, tx, tenant, id)
					switch fault {
					case "agent_source":
						e = injected
					case "agent_missing":
						found = false
					case "agent_retired":
						a.State = "RETIRED"
					case "agent_role":
						a.Roles = nil
					case "agent_family":
						a.ModelFamily = ""
					}
					return a, found, e
				}
			case "runtime_source", "runtime_missing", "runtime_machine", "runtime_offline", "runtime_kind":
				f.m.agentRuntime.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentRuntimeFact, bool, error) {
					r, found, e := runtimeSource(ctx, tx, tenant, id)
					switch fault {
					case "runtime_source":
						e = injected
					case "runtime_missing":
						found = false
					case "runtime_machine":
						r.MachineID = nil
					case "runtime_offline":
						r.MachineState = "OFFLINE"
					case "runtime_kind":
						r.RuntimeKind = "CLAUDE_CODE"
					}
					return r, found, e
				}
			case "id_error":
				f.m.ids = failedExecutionIDs{err: injected}
			case "id_zero":
				f.m.ids = failedExecutionIDs{}
			case "facts_source":
				f.m.planning.AuthorizeOn = func(context.Context, *gorm.DB, delivery.ExecutionWorkLink, string) (delivery.PlanningLaunch, error) {
					return delivery.PlanningLaunch{}, injected
				}
			case "facts_owner", "objective_bounds":
				original := f.m.planning.AuthorizeOn
				f.m.planning.AuthorizeOn = func(ctx context.Context, tx *gorm.DB, l delivery.ExecutionWorkLink, op string) (delivery.PlanningLaunch, error) {
					v, e := original(ctx, tx, l, op)
					if fault == "facts_owner" {
						v.AgentPrincipalID = 22
					} else {
						v.Objective = strings.Repeat("x", 20001)
					}
					return v, e
				}
			case "workspace_source":
				f.m.machines.ResolveWorkspaceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (fleet.WorkspaceFact, error) {
					return fleet.WorkspaceFact{}, injected
				}
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { id, e := f.m.launchPlanningOn(f.human, tx, in); require.Zero(t, id); return e }))
			for _, table := range []string{f.m.names.Executions.String(), "slots", "effects"} {
				var n int64
				require.NoError(t, f.db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}

func TestPlanningReviewLaunchRollsBackBudgetAndSlotDependencyFailures(t *testing.T) {
	for _, fault := range []string{"workspace_missing", "budget", "reserve", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			f := planningReviewFixture(t)
			failure := errors.New("review launch source failed")
			switch fault {
			case "workspace_missing":
				f.m.machines.ResolveWorkspaceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (fleet.WorkspaceFact, error) {
					return fleet.WorkspaceFact{}, fleet.ErrMachineNotFound
				}
			case "budget":
				f.m.work.BudgetOn = func(context.Context, *gorm.DB, WorkLink) (BudgetFact, error) { return BudgetFact{}, failure }
			case "reserve":
				f.m.machines.ReserveSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.SlotFact, error) {
					return fleet.SlotFact{}, failure
				}
			case "outbox":
				f.pub.fail = true
			}
			var before int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&before).Error)
			e := f.db.Transaction(func(tx *gorm.DB) error {
				id, e := f.m.launchPlanningReviewOn(f.human, tx, 7, 81, 22)
				if fault == "workspace_missing" {
					require.Zero(t, id)
				}
				return e
			})
			if fault == "workspace_missing" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
			var after int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&after).Error)
			require.Equal(t, before, after)
		})
	}
}

func TestPlanningReviewEveryStorageFailurePreservesIndependentDecision(t *testing.T) {
	for _, command := range []string{"launch", "pull", "accepted", "complete", "cancel"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f := planningReviewFixture(t)
				var id guid.ID
				if command != "launch" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
						var e error
						id, e = f.m.launchPlanningReviewOn(f.human, tx, 7, 81, 22)
						return e
					}))
				}
				if command == "accepted" || command == "complete" {
					f.pull(t, id)
				}
				if command == "complete" {
					_, e := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
					require.NoError(t, e)
				}
				version := 0
				if command != "launch" {
					version = f.read(t, id).Version
				}
				snapshot := func() map[string][]map[string]any {
					out := map[string][]map[string]any{}
					for _, table := range []string{f.m.names.Executions.String(), f.m.names.Receipts.String(), f.m.names.Logs.String(), f.m.names.Evidence.String(), f.m.names.Approvals.String(), "slots", "effects"} {
						var rows []map[string]any
						require.NoError(t, f.db.Table(table).Order("rowid").Find(&rows).Error)
						out[table] = rows
					}
					return out
				}
				before := snapshot()
				count := 0
				injected := errors.New("execution transactional storage unavailable")
				hook := func(tx *gorm.DB) {
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("execution_transaction_failure", hook))
				case "create":
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("execution_transaction_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("execution_transaction_failure", hook))
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					switch command {
					case "launch":
						_, e := f.m.launchPlanningReviewOn(f.human, tx, 7, 81, 22)
						return e
					case "pull":
						_, e := f.m.PullOn(f.runner, tx, 7, f.fence)
						return e
					case "accepted":
						_, e := f.m.ReceiptOn(f.runner, tx, 7, id, f.fence, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
						return e
					case "complete":
						_, e := f.m.ReceiptOn(f.runner, tx, 7, id, f.fence, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", HeadCommit: strings.Repeat("a", 40), Content: `{"action":"APPROVE","feedback":"Verified current criteria"}`})
						return e
					default:
						_, e := f.m.CancelOn(f.human, tx, 7, id, version)
						return e
					}
				})
				require.NoError(t, f.db.Callback().Query().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Create().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("execution_transaction_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				require.Equal(t, before, snapshot(), "failed command must roll back execution, journals, slots and effects")
				return count
			}
			total := 0
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { total = exercise(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/failure_%d", command, kind, at), func(t *testing.T) { require.GreaterOrEqual(t, exercise(t, at), at) })
			}
		}
	}
}

func planningReviewFixture(t *testing.T) *executionFixture {
	t.Helper()
	f, in := planningExecutionFixture(t)
	sourceID := launchPlanningTest(t, f, in)
	f.pull(t, sourceID)
	_, e := f.receipt(sourceID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, e)
	_, e = f.receipt(sourceID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", HeadCommit: strings.Repeat("a", 40), Content: `{"files":["feature.go"]}`})
	require.NoError(t, e)
	plan := delivery.ReviewPlanView{Content: delivery.ReviewPlanContent{Binding: in.Binding, Producer: &delivery.PlanProducer{ExecutionID: sourceID, AgentPrincipalID: 21, HeadCommit: strings.Repeat("a", 40)}, Files: []string{"feature.go"}}}
	plan.ID = 81
	plan.ProjectID = 9
	plan.WorkItemID = 3
	plan.Version = 1
	plan.ContentHash = strings.Repeat("b", 64)
	f.m.planning.AuthorizeReviewOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (delivery.ReviewPlanView, error) {
		return plan, nil
	}
	f.m.planning.CompleteReviewOn = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID, _ int, _, _, _ string) error {
		return tx.Exec("INSERT INTO effects(kind) VALUES ('review.completed')").Error
	}
	original := f.m.agents.ByPrincipalOn
	f.m.agents.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		a, ok, e := original(ctx, tx, tenant, id)
		if id == 22 {
			a.ModelID = "review-model"
			a.ModelFamily = "independent-family"
		}
		return a, ok, e
	}

	return f
}

func TestPlanningReviewCurrentAuthorityAndRuntimeBeforeReservation(t *testing.T) {
	for _, fault := range []string{"review_missing", "review_source", "producer_missing", "source_missing", "agent_source", "agent_absent", "agent_retired", "agent_role", "agent_family", "runtime_source", "runtime_absent", "runtime_machine", "runtime_offline", "runtime_kind", "workspace_source", "id_error", "id_zero"} {
		t.Run(fault, func(t *testing.T) {
			f := planningReviewFixture(t)
			failure := errors.New("current review source unavailable")
			var before int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&before).Error)
			var priorSlots []map[string]any
			require.NoError(t, f.db.Table("slots").Order("rowid").Find(&priorSlots).Error)
			optional := false
			switch fault {
			case "review_missing":
				f.m.planning.AuthorizeReviewOn = nil
			case "review_source":
				f.m.planning.AuthorizeReviewOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (delivery.ReviewPlanView, error) {
					return delivery.ReviewPlanView{}, failure
				}
			case "producer_missing", "source_missing":
				old := f.m.planning.AuthorizeReviewOn
				f.m.planning.AuthorizeReviewOn = func(ctx context.Context, tx *gorm.DB, a, b, c guid.ID, d bool) (delivery.ReviewPlanView, error) {
					p, e := old(ctx, tx, a, b, c, d)
					if fault == "producer_missing" {
						p.Content.Producer = nil
					} else {
						p.Content.Producer.ExecutionID = 999
					}
					return p, e
				}
			case "agent_source", "agent_absent", "agent_retired", "agent_role", "agent_family":
				old := f.m.agents.ByPrincipalOn
				f.m.agents.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
					a, ok, e := old(ctx, tx, tenant, id)
					switch fault {
					case "agent_source":
						e = failure
					case "agent_absent":
						ok = false
					case "agent_retired":
						a.State = "RETIRED"
					case "agent_role":
						a.Roles = nil
					case "agent_family":
						a.ModelFamily = "family"
					}
					return a, ok, e
				}
			case "runtime_source", "runtime_absent", "runtime_machine", "runtime_offline", "runtime_kind":
				optional = fault != "runtime_source"
				old := f.m.agentRuntime.ByPrincipalOn
				f.m.agentRuntime.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentRuntimeFact, bool, error) {
					a, ok, e := old(ctx, tx, tenant, id)
					switch fault {
					case "runtime_source":
						e = failure
					case "runtime_absent":
						ok = false
					case "runtime_machine":
						a.MachineID = nil
					case "runtime_offline":
						a.MachineState = "OFFLINE"
					case "runtime_kind":
						a.RuntimeKind = "INVALID"
					}
					return a, ok, e
				}
			case "workspace_source":
				f.m.machines.ResolveWorkspaceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (fleet.WorkspaceFact, error) {
					return fleet.WorkspaceFact{}, failure
				}
			case "id_error":
				f.m.ids = failedExecutionIDs{err: failure}
			case "id_zero":
				f.m.ids = failedExecutionIDs{}
			}
			e := f.db.Transaction(func(tx *gorm.DB) error {
				id, e := f.m.launchPlanningReviewOn(f.human, tx, 7, 81, 22)
				require.Zero(t, id)
				return e
			})
			if optional {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
			var after int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&after).Error)
			require.Equal(t, before, after)
			var slots []map[string]any
			require.NoError(t, f.db.Table("slots").Order("rowid").Find(&slots).Error)
			require.Equal(t, priorSlots, slots)
		})
	}
}

func TestPlanningPrincipalDiscoveryIncludesReviewersAndRejectsPartialAuthority(t *testing.T) {
	for _, fault := range []string{"none", "missing_capability", "runtime_error", "runtime_absent", "machine_error", "reviewers_error", "reviewer_runtime_error", "reviewer_unbound", "reviewer_machine_error"} {
		t.Run(fault, func(t *testing.T) {
			f, _ := planningExecutionFixture(t)
			failure := errors.New("planning participant unavailable")
			original := f.m.agentRuntime.ByPrincipalOn
			f.m.agentRuntime.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentRuntimeFact, bool, error) {
				if fault == "runtime_error" || fault == "reviewer_runtime_error" && id == 22 {
					return fleet.AgentRuntimeFact{}, false, failure
				}
				if fault == "runtime_absent" || fault == "reviewer_unbound" && id == 22 {
					return fleet.AgentRuntimeFact{}, false, nil
				}
				r, ok, e := original(ctx, tx, tenant, id)
				if id == 22 {
					machine := guid.ID(2)
					r.MachineID = &machine
				}
				return r, ok, e
			}
			originalMachine := f.m.machines.ByIDOn
			f.m.machines.ByIDOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.MachineFact, error) {
				if fault == "machine_error" || fault == "reviewer_machine_error" && id == 2 {
					return fleet.MachineFact{}, failure
				}
				if id == 2 {
					return fleet.MachineFact{ID: 2, PrincipalID: 102}, nil
				}
				return originalMachine(ctx, tx, tenant, id)
			}
			f.m.agents.ActiveByRoleOn = func(context.Context, *gorm.DB, guid.ID, string) ([]fleet.AgentIdentity, error) {
				if fault == "reviewers_error" {
					return nil, failure
				}
				return []fleet.AgentIdentity{{PrincipalID: 22}, {PrincipalID: 22}}, nil
			}
			if fault == "missing_capability" {
				f.m.agentRuntime.ByPrincipalOn = nil
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				ids, e := f.m.planningPrincipalsOn(f.human, tx, 7, 21)
				if fault == "none" {
					require.Equal(t, []guid.ID{21, 22, 101, 102}, ids)
				}
				if fault == "reviewer_unbound" {
					require.Equal(t, []guid.ID{21, 22, 101}, ids)
				}
				return e
			})
			if fault == "none" || fault == "reviewer_unbound" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPlanningReceiptIllegalTransitionsAndTerminalFailuresPreserveJournal(t *testing.T) {
	for _, fault := range []string{"upload", "progress", "evidence", "heartbeat_before_start", "log_before_start", "repeat_accept", "pause_without_request", "resume_running", "complete_before_start", "bad_report", "unknown_report_field", "missing_complete", "missing_settle", "settle_error", "end_missing", "end_error", "release_error", "negative_usage"} {
		t.Run(fault, func(t *testing.T) {
			f, in := planningExecutionFixture(t)
			id := launchPlanningTest(t, f, in)
			f.pull(t, id)
			receipt := ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"}
			if fault != "heartbeat_before_start" && fault != "log_before_start" && fault != "complete_before_start" {
				_, e := f.receipt(id, receipt)
				require.NoError(t, e)
				receipt.Seq = 2
				receipt.Kind = "HEARTBEAT"
			}
			failure := errors.New("planning terminal dependency failed")
			switch fault {
			case "upload":
				receipt.Upload = &ArtifactUpload{}
			case "progress":
				receipt.Progress = &ProgressObservation{}
			case "evidence":
				receipt.EvidenceKind = "OBSERVATION"
			case "heartbeat_before_start":
				receipt.Kind = "HEARTBEAT"
			case "log_before_start":
				receipt.Kind = "LOG"
				receipt.Content = "runtime output"
			case "repeat_accept":
				receipt.Kind = "ACCEPTED"
			case "pause_without_request":
				receipt.Kind = "PAUSED"
			case "resume_running":
				receipt.Kind = "RESUMED"
			default:
				receipt.Kind = "COMPLETED"
				receipt.HeadCommit = strings.Repeat("a", 40)
				receipt.Content = `{"files":["feature.go"]}`
			}
			switch fault {
			case "bad_report":
				receipt.Content = "{"
			case "unknown_report_field":
				receipt.Content = `{"invented":true}`
			case "missing_complete":
				f.m.planning.CompleteOn = nil
			case "missing_settle":
				f.m.work.SettleBudgetOn = nil
			case "settle_error":
				f.m.work.SettleBudgetOn = func(context.Context, *gorm.DB, WorkLink, WorkResult) error { return failure }
			case "end_missing":
				receipt.Kind = "FAILED"
				f.m.planning.EndOn = nil
			case "end_error":
				receipt.Kind = "FAILED"
				f.m.planning.EndOn = func(context.Context, *gorm.DB, delivery.ExecutionWorkLink, string) error { return failure }
			case "release_error":
				f.m.machines.ReleaseSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return failure }
			case "negative_usage":
				receipt.Usage = &ExecutionUsage{InputTokens: -1}
			}
			before := f.read(t, id)
			var effects, receipts int64
			require.NoError(t, f.db.Table("effects").Count(&effects).Error)
			require.NoError(t, f.db.Table(f.m.names.Receipts.String()).Count(&receipts).Error)
			_, e := f.receipt(id, receipt)
			require.Error(t, e)
			require.Equal(t, before, f.read(t, id))
			var after int64
			require.NoError(t, f.db.Table("effects").Count(&after).Error)
			require.Equal(t, effects, after)
			require.NoError(t, f.db.Table(f.m.names.Receipts.String()).Count(&after).Error)
			require.Equal(t, receipts, after)
			var active int
			require.NoError(t, f.db.Table("slots").Select("active").Where("execution_id=?", id).Scan(&active).Error)
			require.Equal(t, 1, active)
		})
	}
}

func planningContinuationFixture(t *testing.T) (*executionFixture, guid.ID) {
	t.Helper()
	f, in := planningExecutionFixture(t)
	id := launchPlanningTest(t, f, in)
	f.pull(t, id)
	_, e := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, e)
	_, e = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", HeadCommit: strings.Repeat("a", 40), Content: `{"files":["feature.go"]}`})
	require.NoError(t, e)
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
	f.m.planning.ImplementationOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanProducer, error) {
		return delivery.PlanProducer{ExecutionID: id, AgentPrincipalID: 21, Model: "observed-model", Family: "family", HeadCommit: strings.Repeat("a", 40), ApprovedPlan: &delivery.ReviewPlanContent{Files: []string{"feature.go"}}}, nil
	}
	return f, id
}
func TestPlanningContinuationPreservesParentAndEveryStorageFailure(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(fmt.Sprint(approved), func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f, id := planningContinuationFixture(t)
				return f, func(tx *gorm.DB) error {
					child, e := f.m.continuePlanningOn(f.human, tx, 7, 81, id, approved, "Cover the rejected edge case")
					if e == nil {
						r, e := f.m.rowOn(f.human, tx, 7, child, false)
						if e != nil {
							return e
						}
						v, e := view(r)
						require.NoError(t, e)
						require.Equal(t, "CREATED", v.State)
						if approved {
							require.EqualValues(t, 81, v.Plan.ApprovedPlanID)
							require.Equal(t, "WORKSPACE_WRITE", v.Plan.AuthorizationMode)
							require.Equal(t, "Implement the approved frozen execution plan exactly.", v.Plan.Objective)
						} else {
							require.NotNil(t, v.Plan.Planning)
							require.Contains(t, v.Plan.Planning.Feedback, "rejected edge case")
						}
					}
					return e
				}
			})
		})
	}
}
func TestPlanningContinuationCurrentDependencyFailuresNeverLaunchImplementation(t *testing.T) {
	for _, fault := range []string{"source_absent", "source_active", "source_hash", "implementation_missing", "implementation_error", "agent_error", "agent_absent", "agent_retired", "agent_model", "agent_mode", "id_error", "feedback_error", "authorize_error", "workspace_error", "reserve_error", "budget_error", "binding_error"} {
		t.Run(fault, func(t *testing.T) {
			f, id := planningContinuationFixture(t)
			failure := errors.New("continuation dependency unavailable")
			switch fault {
			case "source_absent":
				id = 999
			case "source_active":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Update("state", "RUNNING").Error)
			case "source_hash":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Update("plan_hash", strings.Repeat("f", 64)).Error)
			case "implementation_missing":
				f.m.planning.ImplementationOn = nil
			case "implementation_error":
				f.m.planning.ImplementationOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanProducer, error) {
					return delivery.PlanProducer{}, failure
				}
			case "agent_error", "agent_absent", "agent_retired", "agent_model", "agent_mode":
				old := f.m.agents.ByPrincipalOn
				f.m.agents.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (fleet.AgentIdentity, bool, error) {
					a, ok, e := old(ctx, tx, tenant, agent)
					switch fault {
					case "agent_error":
						e = failure
					case "agent_absent":
						ok = false
					case "agent_retired":
						a.State = "RETIRED"
					case "agent_model":
						a.ModelID = "changed"
					case "agent_mode":
						a.AuthorizationMode = "READ_ONLY"
					}
					return a, ok, e
				}
			case "id_error":
				f.m.ids = failedExecutionIDs{err: failure}
			case "feedback_error":
				f.m.commentFeedback = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (string, error) {
					return "", failure
				}
			case "authorize_error":
				f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return failure }
			case "workspace_error":
				f.m.machines.ResolveWorkspaceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (fleet.WorkspaceFact, error) {
					return fleet.WorkspaceFact{}, failure
				}
			case "reserve_error":
				f.m.machines.ReserveSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.SlotFact, error) {
					return fleet.SlotFact{}, failure
				}
			case "budget_error":
				f.m.work.BudgetOn = func(context.Context, *gorm.DB, WorkLink) (BudgetFact, error) { return BudgetFact{}, failure }
			case "binding_error":
				f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 0, failure }
			}
			var before int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&before).Error)
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.continuePlanningOn(f.human, tx, 7, 81, id, true, ""); return e }))
			var after int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&after).Error)
			require.Equal(t, before, after)
		})
	}
}

func TestPlanningAuthorizationRechecksFrozenFactsCurrentIdentityAndRuntime(t *testing.T) {
	for _, review := range []bool{false, true} {
		for _, fault := range []string{"plan_hash", "authorization_mode", "initiator", "missing_authority", "principal_error", "machine_error", "participants_error", "agent_error", "agent_absent", "agent_model", "runtime_error", "runtime_absent", "runtime_machine", "facts_changed", "review_missing", "review_hash"} {
			t.Run(fmt.Sprintf("review_%t/%s", review, fault), func(t *testing.T) {
				var f *executionFixture
				var id guid.ID
				if review {
					f = planningReviewFixture(t)
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
						var e error
						id, e = f.m.launchPlanningReviewOn(f.human, tx, 7, 81, 22)
						return e
					}))
				} else {
					var in delivery.PlanningLaunch
					f, in = planningExecutionFixture(t)
					id = launchPlanningTest(t, f, in)
				}
				failure := errors.New("planning authority source unavailable")
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					row, e := f.m.rowOn(f.human, tx, 7, id, false)
					require.NoError(t, e)
					switch fault {
					case "plan_hash":
						row.PlanHash = strings.Repeat("f", 64)
					case "authorization_mode":
						v, e := view(row)
						require.NoError(t, e)
						v.Plan.AuthorizationMode = "WORKSPACE_WRITE"
						raw, e := json.Marshal(v.Plan)
						require.NoError(t, e)
						row.PlanJSON = string(raw)
						row.PlanHash = hash(raw)
					case "initiator":
						row.InitiatorID = 11
					case "missing_authority":
						f.m.planning.AuthorizeOn = nil
					case "principal_error":
						f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
					case "machine_error":
						f.m.machines.ByIDOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
							return fleet.MachineFact{}, failure
						}
					case "participants_error":
						f.m.planning.PrincipalsOn = func(context.Context, *gorm.DB, delivery.ExecutionWorkLink) ([]guid.ID, error) { return nil, failure }
					case "agent_error", "agent_absent", "agent_model":
						old := f.m.agents.ByPrincipalOn
						f.m.agents.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (fleet.AgentIdentity, bool, error) {
							a, ok, e := old(ctx, tx, tenant, agent)
							switch fault {
							case "agent_error":
								e = failure
							case "agent_absent":
								ok = false
							case "agent_model":
								a.ModelID = "changed"
							}
							return a, ok, e
						}
					case "runtime_error", "runtime_absent", "runtime_machine":
						old := f.m.agentRuntime.ByPrincipalOn
						f.m.agentRuntime.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (fleet.AgentRuntimeFact, bool, error) {
							a, ok, e := old(ctx, tx, tenant, agent)
							switch fault {
							case "runtime_error":
								e = failure
							case "runtime_absent":
								ok = false
							case "runtime_machine":
								machine := guid.ID(2)
								a.MachineID = &machine
							}
							return a, ok, e
						}
					case "facts_changed":
						old := f.m.planning.AuthorizeOn
						f.m.planning.AuthorizeOn = func(ctx context.Context, tx *gorm.DB, l delivery.ExecutionWorkLink, op string) (delivery.PlanningLaunch, error) {
							v, e := old(ctx, tx, l, op)
							v.Binding = nil
							return v, e
						}
					case "review_missing":
						f.m.planning.AuthorizeReviewOn = nil
					case "review_hash":
						if review {
							old := f.m.planning.AuthorizeReviewOn
							f.m.planning.AuthorizeReviewOn = func(ctx context.Context, tx *gorm.DB, a, b, c guid.ID, d bool) (delivery.ReviewPlanView, error) {
								v, e := old(ctx, tx, a, b, c, d)
								v.ContentHash = "changed"
								return v, e
							}
						}
					}
					_, e = f.m.authorizePlanningOn(f.runner, tx, row, "DISPATCH")
					if !review && (fault == "review_missing" || fault == "review_hash") || review && fault == "facts_changed" {
						require.NoError(t, e)
					} else {
						require.Error(t, e)
					}
					return nil
				}))
			})
		}
	}
}

func TestBindPlanningRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindPlanning(module.NewServices()), module.ErrServicesProviding)
}

func TestSupersededPlannerStorageFailuresNeverCommitPartialCancellation(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f, in := planningExecutionFixture(t)
		id := launchPlanningTest(t, f, in)
		v := f.read(t, id)
		v.Plan.Planning.Review = &PlanningReview{PlanID: 81, Version: 1, ContentHash: "frozen"}
		raw, e := json.Marshal(v.Plan)
		require.NoError(t, e)
		require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Updates(map[string]any{"branch_reason": "PLAN_REVIEW", "plan_json": string(raw), "plan_hash": hash(raw)}).Error)
		f.m.planning.AuthorizeReviewOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (delivery.ReviewPlanView, error) {
			return delivery.ReviewPlanView{}, delivery.ErrPlanNotApproved
		}
		return f, func(tx *gorm.DB) error {
			out, e := f.m.PullOn(f.runner, tx, 7, f.fence)
			if e == nil {
				require.Empty(t, out)
			}
			return e
		}
	})
}
func TestPlanningFailedAndLostReceiptsRequireAtomicSettlement(t *testing.T) {
	for _, kind := range []string{"FAILED", "LOST", StateCanceled} {
		for _, fault := range []string{"settle_missing", "settle_error", "release_error", "usage"} {
			t.Run(kind+"/"+fault, func(t *testing.T) {
				f, in := planningExecutionFixture(t)
				id := launchPlanningTest(t, f, in)
				f.pull(t, id)
				_, e := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
				require.NoError(t, e)
				if kind == StateCanceled {
					version := f.read(t, id).Version
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CancelOn(f.human, tx, 7, id, version); return e }))
				}
				failure := errors.New("planning settlement failed")
				expected := failure
				receipt := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: kind}
				switch fault {
				case "settle_missing":
					f.m.work.SettleBudgetOn = nil
					expected = ErrCapabilityUnavailable
				case "settle_error":
					f.m.work.SettleBudgetOn = func(context.Context, *gorm.DB, WorkLink, WorkResult) error { return failure }
				case "release_error":
					f.m.machines.ReleaseSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return failure }
				case "usage":
					receipt.Usage = &ExecutionUsage{InputTokens: -1}
					expected = ErrInvalidArgument
				}
				before := f.read(t, id)
				_, e = f.receipt(id, receipt)
				require.ErrorIs(t, e, expected)
				require.Equal(t, before, f.read(t, id))
			})
		}
	}
}

func TestSupersededPlanningReviewRequiresCurrentRunnerAndSettlement(t *testing.T) {
	for _, fault := range []string{"session", "settle_missing", "settle_error", "release"} {
		t.Run(fault, func(t *testing.T) {
			f, in := planningExecutionFixture(t)
			id := launchPlanningTest(t, f, in)
			v := f.read(t, id)
			v.Plan.Planning.Review = &PlanningReview{PlanID: 81, Version: 1, ContentHash: "frozen"}
			raw, e := json.Marshal(v.Plan)
			require.NoError(t, e)
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Updates(map[string]any{"branch_reason": "PLAN_REVIEW", "plan_json": string(raw), "plan_hash": hash(raw)}).Error)
			f.m.planning.AuthorizeReviewOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (delivery.ReviewPlanView, error) {
				return delivery.ReviewPlanView{}, delivery.ErrPlanNotApproved
			}
			failure := errors.New("superseded review dependency failed")
			expected := failure
			switch fault {
			case "session":
				f.sessionValid = false
				expected = ErrFenced
			case "settle_missing":
				f.m.work.SettleBudgetOn = nil
				expected = ErrCapabilityUnavailable
			case "settle_error":
				f.m.work.SettleBudgetOn = func(context.Context, *gorm.DB, WorkLink, WorkResult) error { return failure }
			case "release":
				f.m.machines.ReleaseSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return failure }
			}
			before := f.read(t, id)
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.PullOn(f.runner, tx, 7, f.fence); return e }), expected)
			require.Equal(t, before, f.read(t, id))
		})
	}
}

func TestPlanningTerminalDependenciesNeverAdvanceSequenceOnFailure(t *testing.T) {
	for _, fault := range []string{"complete_missing", "complete_error", "settle_missing", "settle_error", "release_error", "end_missing", "end_error", "id_error", "principal_error", "session_error", "usage"} {
		t.Run(fault, func(t *testing.T) {
			f, in := planningExecutionFixture(t)
			id := launchPlanningTest(t, f, in)
			f.pull(t, id)
			_, e := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, e)
			before := f.read(t, id)
			failure := errors.New("planning terminal dependency failed")
			receipt := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", HeadCommit: strings.Repeat("a", 40), Content: `{"files":["feature.go"]}`}
			switch fault {
			case "complete_missing":
				f.m.planning.CompleteOn = nil
			case "complete_error":
				f.m.planning.CompleteOn = func(context.Context, *gorm.DB, delivery.ExecutionWorkLink, delivery.PlanProducer, delivery.ReviewPlanContent) (delivery.ReviewPlanView, error) {
					return delivery.ReviewPlanView{}, failure
				}
			case "settle_missing":
				f.m.work.SettleBudgetOn = nil
			case "settle_error":
				f.m.work.SettleBudgetOn = func(context.Context, *gorm.DB, WorkLink, WorkResult) error { return failure }
			case "release_error":
				f.m.machines.ReleaseSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return failure }
			case "end_missing":
				receipt.Kind = "FAILED"
				f.m.planning.EndOn = nil
			case "end_error":
				receipt.Kind = "FAILED"
				f.m.planning.EndOn = func(context.Context, *gorm.DB, delivery.ExecutionWorkLink, string) error { return failure }
			case "id_error":
				f.m.ids = failedExecutionIDs{err: failure}
			case "principal_error":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "session_error":
				f.sessionValid = false
			case "usage":
				receipt.Usage = &ExecutionUsage{InputTokens: -1}
			}
			_, e = f.receipt(id, receipt)
			require.Error(t, e)
			require.Equal(t, before, f.read(t, id))
		})
	}
}
