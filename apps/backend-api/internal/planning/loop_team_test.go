package planning

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"slices"
)

func bindTeamLoopFacts(t *testing.T, h harness, cost *int64, settled *bool) {
	t.Helper()
	bindLoopFacts(h, cost, settled)
	h.m.loopRuntime.executions.AgentOn = func(_ context.Context, _ *gorm.DB, tenant, agent guid.ID, start, end time.Time) ([]execution.LoopExecutionFact, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(7), agent)
		at := start
		return []execution.LoopExecutionFact{{ID: 50, ProjectID: 3, AssignmentID: 60, StartedAt: &at}}, nil
	}
}

func TestProjectLoopDraftArchiveFencesWritesAndPreservesHistory(t *testing.T) {
	h := loopHarness(t)
	draft := createTestLoop(t, h, false)
	require.Equal(t, "DRAFT", draft.State)
	require.Nil(t, draft.ReminderID)
	reads := 0
	h.m.project.ArchivedOn = func(_ context.Context, tx *gorm.DB, tenant, project guid.ID) (bool, error) {
		_, active := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, active)
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		reads++
		return true, nil
	}
	count := func(table string) int64 {
		var n int64
		require.NoError(t, h.db.Table(table).Count(&n).Error)
		return n
	}
	auditBefore, eventsBefore := count("audit_probe"), count("p_domain_events")
	change := func(tx *gorm.DB) error {
		_, err := h.m.changeLoopStateOn(claims(t, 2), tx, 1, draft.ID, draft.Version, "ARCHIVED")
		return err
	}
	require.ErrorIs(t, h.db.Transaction(change), ErrConflict)
	require.Equal(t, 1, reads)
	require.Equal(t, auditBefore, count("audit_probe"))
	require.Equal(t, eventsBefore, count("p_domain_events"))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		r, err := h.m.commandLoopOn(claims(t, 2), tx, 1, draft.ID, false)
		require.NoError(t, err)
		require.Equal(t, "DRAFT", r.State)
		require.Equal(t, draft.Version, r.Version)
		return err
	}))
	require.Equal(t, 1, reads, "history does not require an active project")
	contractsBefore := count(h.m.names.LoopContracts.String())
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.createLoopOn(claims(t, 2), tx, 1, testLoopDefinition(), false)
		return err
	}), ErrConflict)
	require.Equal(t, contractsBefore, count(h.m.names.LoopContracts.String()))
	require.Equal(t, auditBefore, count("audit_probe"))
	require.Equal(t, eventsBefore, count("p_domain_events"))
	h.m.project.ArchivedOn = nil
	require.ErrorIs(t, h.db.Transaction(change), ErrUnavailable)
	h.m.project.ArchivedOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil }
	require.NoError(t, h.db.Transaction(change), "existing owner authority remains sufficient on active projects")
}

func TestTeamLoopCostsUseActualProjectSettlementsAndFailClosed(t *testing.T) {
	h := loopHarness(t)
	cost := int64(4)
	settled := true
	bindTeamLoopFacts(t, h, &cost, &settled)
	start, end := h.m.now(), h.m.now().Add(time.Minute)
	facts := []execution.LoopExecutionFact{{ID: 50, ProjectID: 3, AssignmentID: 60, StartedAt: &start}, {ID: 51, ProjectID: 4, AssignmentID: 61, StartedAt: &start}}
	h.m.loopRuntime.executions.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) ([]execution.LoopExecutionFact, error) {
		return facts, nil
	}
	projects := []guid.ID{}
	h.m.loopRuntime.assignments.AgentsOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, ids []guid.ID) (map[guid.ID]guid.ID, error) {
		require.Equal(t, guid.ID(1), tenant)
		return map[guid.ID]guid.ID{60: 7, 61: 7}, nil
	}
	h.m.loopRuntime.settlements.ExecutionsOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, ids []guid.ID) (int64, bool, error) {
		require.Equal(t, guid.ID(1), tenant)
		projects = append(projects, project)
		return cost, settled, nil
	}
	contract := &model.LoopContract{TenantID: 1, AgentID: 7}
	read := func() (int64, error) {
		var value int64
		err := h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			value, err = h.m.loopCostOn(t.Context(), tx, contract, start, end)
			return err
		})
		return value, err
	}
	value, err := read()
	require.NoError(t, err)
	require.Equal(t, int64(8), value)
	require.Equal(t, []guid.ID{3, 4}, projects)
	settled = false
	_, err = read()
	require.ErrorIs(t, err, ErrUnavailable)
	settled = true
	cost = math.MaxInt64
	_, err = read()
	require.ErrorIs(t, err, ErrUnavailable)
	cost = 4
	for _, mutate := range []func(){func() { facts[0].StartedAt = nil }, func() { facts[0].ProjectID = 0 }, func() { facts[0].StartedAt = &end }, func() { facts[1].ID = facts[0].ID }} {
		original := append([]execution.LoopExecutionFact(nil), facts...)
		mutate()
		_, err = read()
		require.ErrorIs(t, err, ErrUnavailable)
		facts = original
	}
	h.m.loopRuntime.assignments.AgentsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (map[guid.ID]guid.ID, error) {
		return map[guid.ID]guid.ID{60: 9, 61: 7}, nil
	}
	_, err = read()
	require.ErrorIs(t, err, ErrUnavailable, "historical executor cannot be replaced")
	failure := errors.New("real execution facts unavailable")
	h.m.loopRuntime.executions.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) ([]execution.LoopExecutionFact, error) {
		return nil, failure
	}
	_, err = read()
	require.ErrorIs(t, err, failure)
}

func TestTeamLoopLifecycleUsesNullReminderAndAtomicBudgetPause(t *testing.T) {
	for _, actual := range []int64{4, 6} {
		t.Run(map[int64]string{4: "success", 6: "budget"}[actual], func(t *testing.T) {
			h := loopHarness(t)
			cost := actual
			settled := true
			bindTeamLoopFacts(t, h, &cost, &settled)
			h.m.loopReminders.UpsertOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, in agents.LoopReminderInput) (guid.ID, error) {
				require.Nil(t, in.ProjectID)
				return 500, tx.Exec("INSERT INTO scheduler_probe VALUES (?,?)", 500, "ACTIVE").Error
			}
			def := testLoopDefinition()
			def.ProjectID = nil
			def.Budget.MaxCostMicrosPerRun = 5
			def.Escalation.OnBudgetExceeded = true
			def.Escalation.ConsecutiveFailuresToPause = 1
			var contract LoopContractView
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				var err error
				contract, err = h.m.createLoopOn(claims(t, 2), tx, 1, def, true)
				return err
			}))
			require.Nil(t, contract.ProjectID)
			require.Empty(t, contract.UnavailableCapabilities)
			var run LoopRunView
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				var err error
				run, _, err = h.m.openLoopRunOn(t.Context(), tx, 1, contract.ID, 1, h.m.now())
				return err
			}))
			at := h.m.now().Add(time.Minute)
			h.m.now = func() time.Time { return at }
			input := &LoopResultInput{}
			require.NoError(t, json.Unmarshal([]byte(`{"self_check_results":[{"key":"evidence","passed":true}],"check_result":{"status":"OK"}}`), &input.Body))
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
				_, _, err := h.m.authorizeLoopResultOn(claims(t, 2), tx, 1, run.ID)
				return err
			}), ErrForbidden)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				c, r, err := h.m.authorizeLoopResultOn(claims(t, 7), tx, 1, run.ID)
				if err != nil {
					return err
				}
				out, err := h.m.completeLoopOn(claims(t, 7), tx, c, r, input)
				if err == nil {
					if actual > 5 {
						require.Equal(t, "ESCALATED", out.State)
						require.Equal(t, "BUDGET_EXCEEDED", *out.EscalationReason)
					} else {
						require.Equal(t, "SUCCEEDED", out.State)
						require.Nil(t, out.EscalationReason)
					}
					require.Equal(t, actual, *out.CostMicros)
				}
				return err
			}))
			persisted, err := h.m.loopOn(t.Context(), h.db, 1, contract.ID)
			require.NoError(t, err)
			require.Nil(t, persisted.ProjectID)
			require.Equal(t, map[bool]string{true: "PAUSED", false: "ACTIVE"}[actual > 5], persisted.State)
		})
	}
}

func TestTeamCalendarAndTimeoutRetainTenantScope(t *testing.T) {
	h := loopHarness(t)
	cost := int64(0)
	settled := true
	bindTeamLoopFacts(t, h, &cost, &settled)
	def := testLoopDefinition()
	def.ProjectID = nil
	kind := "DAY"
	def.ScheduleKind = "PERIOD"
	def.IntervalMinutes = nil
	def.PeriodKind = &kind
	next := guid.ID(500)
	h.m.loopReminders.UpsertOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, in agents.LoopReminderInput) (guid.ID, error) {
		require.Nil(t, in.ProjectID)
		require.NotNil(t, in.DueAt)
		next++
		return next, tx.Exec("INSERT INTO scheduler_probe VALUES (?,?)", next, "ACTIVE").Error
	}
	var contract LoopContractView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		contract, err = h.m.createLoopOn(claims(t, 2), tx, 1, def, true)
		return err
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.openLoopReminderOn(t.Context(), tx, 1, *contract.ReminderID, 1, h.m.now())
		return err
	}))
	at := h.m.now().Add(31 * time.Minute)
	h.m.now = func() time.Time { return at }
	require.NoError(t, h.m.scanLoopTimeouts(t.Context(), 1))
	var state string
	require.NoError(t, h.db.Table(h.m.names.LoopRuns.String()).Select("state").Scan(&state).Error)
	require.Equal(t, "TIMED_OUT", state)
}

type teamOrderMembers struct {
	core.ActiveMemberChecker
	check func()
}

func (m teamOrderMembers) IsMemberActiveOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) (bool, error) {
	m.check()
	return m.ActiveMemberChecker.IsMemberActiveOn(ctx, tx, tenant, principal)
}

func TestTeamLoopParticipantsLockBeforeIAMAndContract(t *testing.T) {
	for _, operation := range []string{"create", "pause", "patch"} {
		t.Run(operation, func(t *testing.T) {
			h := loopHarness(t)
			def := testLoopDefinition()
			def.ProjectID = nil
			var contract LoopContractView
			if operation != "create" {
				require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
					var err error
					contract, err = h.m.createLoopOn(claims(t, 2), tx, 1, def, true)
					return err
				}))
			}
			locked := []guid.ID{}
			fenced := false
			original := h.m.principals.LockActiveRow
			h.m.principals.LockActiveRow = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				if fenced {
					require.True(t, slices.Contains(locked, id), "new principal %s locked after IAM fence", id)
				}
				locked = append(locked, id)
				return original(ctx, tx, id)
			}
			assertOrder := func() {
				require.GreaterOrEqual(t, len(locked), 2)
				require.Equal(t, []guid.ID{2, 7}, locked[:2])
				fenced = true
			}
			h.m.members = teamOrderMembers{ActiveMemberChecker: h.m.members, check: assertOrder}
			require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("team_contract_lock_order", func(db *gorm.DB) {
				if db.Statement.Table == h.m.names.LoopContracts.String() {
					if _, ok := db.Statement.Clauses["FOR"]; ok {
						assertOrder()
					}
				}
			}))
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				switch operation {
				case "create":
					_, err := h.m.createLoopOn(claims(t, 2), tx, 1, def, true)
					return err
				case "pause":
					_, err := h.m.changeLoopStateOn(claims(t, 2), tx, 1, contract.ID, contract.Version, "PAUSED")
					return err
				default:
					name := "Updated team loop"
					_, err := h.m.patchLoopOn(claims(t, 2), tx, 1, contract.ID, contract.Version, LoopPatch{Name: &name})
					return err
				}
			}))
		})
	}
}

func TestTeamLoopRevalidatesObservedAgentOwnerAfterPrincipalLocks(t *testing.T) {
	for _, changeAt := range []int{2, 3, 4} {
		t.Run(map[int]string{2: "before_helper_snapshot", 3: "after_principal_locks", 4: "after_helper_recheck"}[changeAt], func(t *testing.T) {
			h := loopHarness(t)
			original := h.m.agents.ByPrincipalOn
			calls := 0
			h.m.agents.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
				agent, found, err := original(ctx, tx, tenant, id)
				calls++
				if calls >= changeAt {
					agent.OwnerPrincipalID = 9
				}
				return agent, found, err
			}
			def := testLoopDefinition()
			def.ProjectID = nil
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.createLoopOn(claims(t, 2), tx, 1, def, true); return err }), ErrConflict)
			var count int64
			require.NoError(t, h.db.Table(h.m.names.LoopContracts.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestTeamLoopParticipantFailuresRollBackBeforeContractOrReminder(t *testing.T) {
	for _, fault := range []string{"missing_lock", "missing_agent", "agent_read_failure", "lock_failure", "agent_recheck_failure", "agent_disappeared"} {
		t.Run(fault, func(t *testing.T) {
			h := loopHarness(t)
			failure := errors.New("team participant source failure")
			original := h.m.agents.ByPrincipalOn
			calls := 0
			if fault == "missing_lock" {
				h.m.principals.LockActiveRow = nil
			}
			if fault == "lock_failure" {
				h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			}
			h.m.agents.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
				calls++
				if calls == 2 && fault == "missing_agent" {
					return fleet.AgentIdentity{}, false, nil
				}
				if calls == 2 && fault == "agent_read_failure" || calls == 3 && fault == "agent_recheck_failure" {
					return fleet.AgentIdentity{}, false, failure
				}
				if calls == 3 && fault == "agent_disappeared" {
					return fleet.AgentIdentity{}, false, nil
				}
				return original(ctx, tx, tenant, id)
			}
			def := testLoopDefinition()
			def.ProjectID = nil
			err := h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.createLoopOn(claims(t, 2), tx, 1, def, true); return err })
			require.Error(t, err)
			if slices.Contains([]string{"agent_read_failure", "lock_failure", "agent_recheck_failure"}, fault) {
				require.ErrorIs(t, err, failure)
			}
			for _, table := range []string{h.m.names.LoopContracts.String(), "scheduler_probe"} {
				var count int64
				require.NoError(t, h.db.Table(table).Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}
}

func TestTeamReportLoopBindsTenantReportWithoutAutoSubmission(t *testing.T) {
	h := loopHarness(t)
	cost := int64(0)
	settled := true
	bindTeamLoopFacts(t, h, &cost, &settled)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
	goal := goalTree(t, h)
	report := reportDraft(t, h)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", goal.ID).Update("principal_id", 7).Error)
	h.facts.Work[0].AccountablePrincipalID = 7
	body := PlanningReportInput{Completed: []PlanningCompletion{{WorkItemID: 100}}, Uncompleted: "Remaining work", Retrospective: "Actual accepted evidence", NextPlan: []PlanningPlanItem{linkedItem(goal)}, DeclaredConfidence: "GREEN", StatusExplanation: "The accepted work supports recovery despite the current goal lag."}
	encoded, hash, e := hashJSON(body)
	require.NoError(t, e)
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", report.ID).Updates(map[string]any{"project_id": 0, "principal_id": 7, "content": encoded, "content_hash": hash, "state": "READY"}).Error)
	def := testLoopDefinition()
	def.ProjectID = nil
	def.Kind = "REPORT"
	def.OutputFormat = "REPORT_DRAFT"
	def.ReportBinding = &LoopReportBinding{Level: "MEMBER", PeriodKind: "WEEK"}
	var contract LoopContractView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		contract, e = h.m.createLoopOn(claims(t, 2), tx, 1, def, true)
		return e
	}))
	var run LoopRunView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		run, _, e = h.m.openLoopRunOn(t.Context(), tx, 1, contract.ID, 1, h.m.now())
		return e
	}))
	now := h.m.now().Add(time.Minute)
	h.m.now = func() time.Time { return now }
	in := &LoopResultInput{}
	require.NoError(t, json.Unmarshal([]byte(`{"self_check_results":[{"key":"evidence","passed":true}]}`), &in.Body))
	in.Body.ReportID = &report.ID
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		c, r, e := h.m.authorizeLoopResultOn(claims(t, 7), tx, 1, run.ID)
		if e != nil {
			return e
		}
		row, _, e := h.m.reportOn(claims(t, 7), tx, 1, report.ID, "report_read", false)
		if e != nil {
			return e
		}
		check, e := h.m.checkReport(claims(t, 7), tx, 1, row)
		require.NoError(t, e)
		require.NotEqual(t, "BLOCKED", check.Result, "issues: %+v", check.Issues)

		out, e := h.m.completeLoopOn(claims(t, 7), tx, c, r, in)
		require.Equal(t, "SUCCEEDED", out.State)
		return e
	}))
	var state string
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Select("state").Where("id=?", report.ID).Scan(&state).Error)
	require.Equal(t, "READY", state)
}
