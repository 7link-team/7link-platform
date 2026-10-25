package planning

import (
	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification"
	"7link.com/platform/internal/planning/model"
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

func bindLoopFacts(h harness, cost *int64, settled *bool) {
	h.m.loopRuntime = loopRuntimeServices{
		executions: execution.LoopExecutionFacts{ReadOn: func(_ context.Context, _ *gorm.DB, _, _ guid.ID, _ []guid.ID, start, end time.Time) ([]execution.LoopExecutionFact, error) {
			at := start.Add(time.Millisecond)
			return []execution.LoopExecutionFact{{ID: 50, AssignmentID: 60, StartedAt: &at}}, nil
		}},
		assignments: delivery.LoopAssignments{AgentsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (map[guid.ID]guid.ID, error) {
			return map[guid.ID]guid.ID{60: 7}, nil
		}},
		settlements: budget.SettlementFacts{ExecutionsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) {
			return *cost, *settled, nil
		}},
		projects: delivery.ProjectBudgets{LockOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ProjectBudget, error) {
			return delivery.ProjectBudget{}, nil
		}},
	}
}

func TestLoopCompletionUsesSettledFactsAndAtomicEscalation(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "budget"}[failed], func(t *testing.T) {
			h := loopHarness(t)
			cost := int64(5)
			settled := false
			bindLoopFacts(h, &cost, &settled)
			def := testLoopDefinition()
			def.Budget.MaxCostMicrosPerRun = 10
			def.Escalation.OnBudgetExceeded = true
			def.Escalation.ConsecutiveFailuresToPause = 1
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
			at := h.m.now().Add(time.Minute)
			h.m.now = func() time.Time { return at }
			in := &LoopResultInput{ID: run.ID.String(), Key: "result-one"}
			require.NoError(t, json.Unmarshal([]byte(`{"self_check_results":[{"key":"evidence","passed":true}],"check_result":{"status":"OK"}}`), &in.Body))
			complete := func() (LoopRunView, error) {
				var out LoopRunView
				e := h.db.Transaction(func(tx *gorm.DB) error {
					c, r, e := h.m.authorizeLoopResultOn(claims(t, 7), tx, 1, run.ID)
					if e != nil {
						return e
					}
					out, e = h.m.completeLoopOn(claims(t, 7), tx, c, r, in)
					return e
				})
				return out, e
			}
			_, e := complete()
			require.ErrorIs(t, e, ErrUnavailable)
			settled = true
			if failed {
				cost = 11
			}
			out, e := complete()
			require.NoError(t, e)
			require.NotNil(t, out.CostMicros)
			require.Equal(t, cost, *out.CostMicros)
			if failed {
				require.Equal(t, "ESCALATED", out.State)
				require.Equal(t, "BUDGET_EXCEEDED", *out.EscalationReason)
			} else {
				require.Equal(t, "SUCCEEDED", out.State)
			}
			_, e = complete()
			require.ErrorIs(t, e, ErrConflict)
			c, e := h.m.loopOn(t.Context(), h.db, 1, contract.ID)
			require.NoError(t, e)
			if failed {
				require.Equal(t, "PAUSED", c.State)
			} else {
				require.Equal(t, "ACTIVE", c.State)
			}
		})
	}
}
func TestLoopTimeoutPersistsWithoutInventingUnsettledCost(t *testing.T) {
	h := loopHarness(t)
	cost := int64(99)
	settled := false
	bindLoopFacts(h, &cost, &settled)
	contract := createTestLoop(t, h, true)
	var run LoopRunView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		run, _, e = h.m.openLoopRunOn(t.Context(), tx, 1, contract.ID, 1, h.m.now())
		return e
	}))
	require.NoError(t, h.m.scanLoopTimeouts(t.Context(), 1))
	var state string
	require.NoError(t, h.db.Table(h.m.names.LoopRuns.String()).Select("state").Where("id=?", run.ID).Scan(&state).Error)
	require.Equal(t, "OPEN", state)
	now := h.m.now().Add(31 * time.Minute)
	h.m.now = func() time.Time { return now }
	require.NoError(t, h.m.scanLoopTimeouts(t.Context(), 1))
	require.NoError(t, h.m.scanLoopTimeouts(t.Context(), 1))
	require.NoError(t, h.db.Table(h.m.names.LoopRuns.String()).Select("state").Where("id=?", run.ID).Scan(&state).Error)
	require.Equal(t, "TIMED_OUT", state)
	require.ErrorIs(t, h.m.scanLoopTimeouts(t.Context(), 0), ErrInvalid)
}
func TestLoopResultValidationAndAuditFailureLeaveRunOpen(t *testing.T) {
	h := loopHarness(t)
	cost := int64(5)
	settled := true
	bindLoopFacts(h, &cost, &settled)
	contract := createTestLoop(t, h, true)
	var run LoopRunView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		run, _, e = h.m.openLoopRunOn(t.Context(), tx, 1, contract.ID, 1, h.m.now())
		return e
	}))
	now := h.m.now().Add(time.Minute)
	h.m.now = func() time.Time { return now }
	for _, body := range []string{`{}`, `{"self_check_results":[{"key":"unknown","passed":true}],"check_result":{"status":"OK"}}`, `{"self_check_results":[{"key":"evidence","passed":true},{"key":"evidence","passed":true}],"check_result":{"status":"OK"}}`, `{"self_check_results":[{"key":"evidence","passed":true}],"check_result":{"status":"BOGUS"}}`, `{"self_check_results":[{"key":"evidence","passed":true}],"report_id":"123","check_result":{"status":"OK"}}`} {
		in := &LoopResultInput{}
		require.NoError(t, json.Unmarshal([]byte(body), &in.Body))
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
			c, r, e := h.m.authorizeLoopResultOn(claims(t, 7), tx, 1, run.ID)
			if e != nil {
				return e
			}
			_, e = h.m.completeLoopOn(claims(t, 7), tx, c, r, in)
			return e
		}), ErrInvalid)
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, _, e := h.m.authorizeLoopResultOn(claims(t, 2), tx, 1, run.ID); return e }), ErrForbidden)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, _, e := h.m.authorizeLoopResultOn(claims(t, 7), tx, 1, 999); return e }), ErrNotFound)
	failure := errors.New("audit unavailable")
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	in := &LoopResultInput{}
	require.NoError(t, json.Unmarshal([]byte(`{"self_check_results":[{"key":"evidence","passed":false}],"check_result":{"status":"FAIL"}}`), &in.Body))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		c, r, e := h.m.authorizeLoopResultOn(claims(t, 7), tx, 1, run.ID)
		if e != nil {
			return e
		}
		_, e = h.m.completeLoopOn(claims(t, 7), tx, c, r, in)
		return e
	}), failure)
	var state string
	require.NoError(t, h.db.Table(h.m.names.LoopRuns.String()).Select("state").Where("id=?", run.ID).Scan(&state).Error)
	require.Equal(t, "OPEN", state)
}
func TestLoopCostRejectsUnknownAssignmentOrStart(t *testing.T) {
	h := loopHarness(t)
	cost := int64(5)
	settled := true
	bindLoopFacts(h, &cost, &settled)
	project := guid.ID(3)
	contract := &model.LoopContract{TenantID: 1, ProjectID: &project, AgentID: 7}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		h.m.loopRuntime.executions.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]execution.LoopExecutionFact, error) {
			return []execution.LoopExecutionFact{{ID: 50, AssignmentID: 60}}, nil
		}
		_, e := h.m.loopCostOn(t.Context(), tx, contract, h.m.now(), h.m.now())
		require.ErrorIs(t, e, ErrUnavailable)
		h.m.loopRuntime.assignments.AgentsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (map[guid.ID]guid.ID, error) {
			return map[guid.ID]guid.ID{}, nil
		}
		_, e = h.m.loopCostOn(t.Context(), tx, contract, h.m.now(), h.m.now())
		require.ErrorIs(t, e, ErrUnavailable)
		contract.ProjectID = nil
		_, e = h.m.loopCostOn(t.Context(), tx, contract, h.m.now(), h.m.now())
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
}
func TestLoopMonthlyCapPausesUsingActualSettledCosts(t *testing.T) {
	h := loopHarness(t)
	cost := int64(7)
	settled := true
	bindLoopFacts(h, &cost, &settled)
	def := testLoopDefinition()
	monthlyCap := int64(6)
	def.Budget.MonthlyCostCapMicros = &monthlyCap
	def.Budget.MaxCostMicrosPerRun = 10
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
	require.NoError(t, json.Unmarshal([]byte(`{"self_check_results":[{"key":"evidence","passed":true}],"check_result":{"status":"OK"}}`), &in.Body))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		c, r, e := h.m.authorizeLoopResultOn(claims(t, 7), tx, 1, run.ID)
		if e != nil {
			return e
		}
		out, e := h.m.completeLoopOn(claims(t, 7), tx, c, r, in)
		require.Equal(t, "MONTHLY_CAP_EXCEEDED", *out.EscalationReason)
		return e
	}))
	c, e := h.m.loopOn(t.Context(), h.db, 1, contract.ID)
	require.NoError(t, e)
	require.Equal(t, "PAUSED", c.State)
}
func TestLoopResultHTTPReplaysOnlyForCurrentAgentAuthority(t *testing.T) {
	h := loopHarness(t)
	cost := int64(5)
	settled := true
	bindLoopFacts(h, &cost, &settled)
	contract := createTestLoop(t, h, true)
	var run LoopRunView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		run, _, e = h.m.openLoopRunOn(t.Context(), tx, 1, contract.ID, 1, h.m.now())
		return e
	}))
	now := h.m.now().Add(time.Minute)
	h.m.now = func() time.Time { return now }
	in := &LoopResultInput{ID: run.ID.String(), Key: "result-one"}
	require.NoError(t, json.Unmarshal([]byte(`{"self_check_results":[{"key":"evidence","passed":true}],"check_result":{"status":"OK"}}`), &in.Body))
	first, e := h.m.recordLoopResultHTTP(claims(t, 7), in)
	require.NoError(t, e)
	again, e := h.m.recordLoopResultHTTP(claims(t, 7), in)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data, again.Body.Data)
	_, e = h.m.recordLoopResultHTTP(claims(t, 2), in)
	require.Error(t, e)
	h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	_, e = h.m.recordLoopResultHTTP(claims(t, 7), in)
	require.Error(t, e)
}
func TestReportLoopBindsActualReportAndPreservesDraftState(t *testing.T) {
	h := loopHarness(t)
	cost := int64(5)
	settled := true
	bindLoopFacts(h, &cost, &settled)
	report := reportDraft(t, h)
	def := testLoopDefinition()
	def.Kind = "REPORT"
	def.OutputFormat = "REPORT_DRAFT"
	def.ReportBinding = &LoopReportBinding{Level: "MEMBER", PeriodKind: "WEEK"}
	def.Budget.MaxCostMicrosPerRun = 10
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
	complete := func() (LoopRunView, error) {
		var out LoopRunView
		e := h.db.Transaction(func(tx *gorm.DB) error {
			c, r, e := h.m.authorizeLoopResultOn(claims(t, 7), tx, 1, run.ID)
			if e != nil {
				return e
			}
			out, e = h.m.completeLoopOn(claims(t, 7), tx, c, r, in)
			return e
		})
		return out, e
	}
	_, e := complete()
	require.ErrorIs(t, e, ErrForbidden, "the Agent cannot read another principal's report")
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", report.ID).Updates(map[string]any{"principal_id": 7, "period_kind": "DAY"}).Error)
	_, e = complete()
	require.ErrorIs(t, e, ErrInvalid, "an authorized report must still match the loop period binding")
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", report.ID).Update("period_kind", "WEEK").Error)
	out, e := complete()
	require.NoError(t, e)
	require.Equal(t, "ESCALATED", out.State)
	require.Equal(t, "REPORT_BLOCKED", *out.EscalationReason)
	var state string
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Select("state").Where("id=?", report.ID).Scan(&state).Error)
	require.Equal(t, "DRAFT", state)
}
func TestReportLoopAllowsReadyReportWithNonblockingFlags(t *testing.T) {
	h := loopHarness(t)
	cost := int64(0)
	settled := true
	bindLoopFacts(h, &cost, &settled)
	goal := goalTree(t, h)
	report := reportDraft(t, h)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", goal.ID).Update("principal_id", 7).Error)
	h.facts.Work[0].AccountablePrincipalID = 7
	body := PlanningReportInput{Completed: []PlanningCompletion{{WorkItemID: 100}}, Uncompleted: "Remaining work", Retrospective: "Actual accepted evidence", NextPlan: []PlanningPlanItem{linkedItem(goal)}, DeclaredConfidence: "GREEN", StatusExplanation: "The accepted work supports recovery despite the current goal lag."}
	encoded, hash, e := hashJSON(body)
	require.NoError(t, e)
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", report.ID).Updates(map[string]any{"principal_id": 7, "content": encoded, "content_hash": hash, "state": "READY"}).Error)
	def := testLoopDefinition()
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
		require.Equal(t, "PASS_WITH_FLAGS", check.Result, "issues: %+v", check.Issues)
		require.Contains(t, check.Issues, PlanningIssue{Code: "N5_STATUS_EXPLAINED", Blocking: false})
		out, e := h.m.completeLoopOn(claims(t, 7), tx, c, r, in)
		require.Equal(t, "SUCCEEDED", out.State)
		return e
	}))
	var state string
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Select("state").Where("id=?", report.ID).Scan(&state).Error)
	require.Equal(t, "READY", state)
}
func TestLoopMonthlyLedgerCountsOtherRunsAndProtectsOverflow(t *testing.T) {
	h := loopHarness(t)
	cost := int64(7)
	settled := true
	bindLoopFacts(h, &cost, &settled)
	def := testLoopDefinition()
	def.AllowOverlap = true
	monthlyCap := int64(10)
	def.Budget.MonthlyCostCapMicros = &monthlyCap
	var contract LoopContractView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		contract, e = h.m.createLoopOn(claims(t, 2), tx, 1, def, true)
		return e
	}))
	var run LoopRunView
	for fire := 1; fire <= 2; fire++ {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			run, _, e = h.m.openLoopRunOn(t.Context(), tx, 1, contract.ID, fire, h.m.now())
			return e
		}))
	}
	now := h.m.now().Add(time.Minute)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		c, e := h.m.loopOn(t.Context(), tx, 1, contract.ID)
		if e != nil {
			return e
		}
		var row model.LoopRun
		require.NoError(t, tx.Table(h.m.names.LoopRuns.String()).Where("id=?", run.ID).Take(&row).Error)
		exceeded, e := h.m.loopMonthlyExceededOn(t.Context(), tx, c, &row, contract, 7, now)
		require.NoError(t, e)
		require.True(t, exceeded)
		_, e = h.m.loopMonthlyExceededOn(t.Context(), tx, c, &row, contract, math.MaxInt64, now)
		require.ErrorIs(t, e, ErrInvalid)
		return nil
	}))
}

// Every downstream failure must leave both the run and its scheduler fence intact.
func operationalLoopFixture(t *testing.T) (harness, LoopContractView, LoopRunView, *LoopResultInput) {
	t.Helper()
	h := loopHarness(t)
	cost := int64(1)
	settled := true
	bindLoopFacts(h, &cost, &settled)
	def := testLoopDefinition()
	def.Budget.MaxCostMicrosPerRun = 10
	def.Escalation.ConsecutiveFailuresToPause = 1
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
	require.NoError(t, json.Unmarshal([]byte(`{"self_check_results":[{"key":"evidence","passed":false}],"check_result":{"status":"FAIL"}}`), &in.Body))
	return h, contract, run, in
}

type loopFaultNotifier struct {
	notification.Notifier
	failType string
}

func (n loopFaultNotifier) Notify(ctx context.Context, tx *gorm.DB, in notification.Notification) ([]guid.ID, error) {
	ids, e := n.Notifier.Notify(ctx, tx, in)
	if e != nil {
		return nil, e
	}
	if in.Type == n.failType {
		return nil, errors.New("notification unavailable")
	}
	return ids, nil
}
func TestLoopCurrentAuthorityAndSourceFailureNeverCompleteRun(t *testing.T) {
	failure := errors.New("source unavailable")
	cases := []struct {
		name   string
		mutate func(harness) error
	}{
		{"missing principal lock", func(h harness) error { h.m.principals.LockActiveRow = nil; return ErrUnavailable }},
		{"principal lock failure", func(h harness) error {
			h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, false, failure
			}
			return failure
		}},
		{"project fence failure", func(h harness) error {
			h.m.loopRuntime.projects.LockOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ProjectBudget, error) {
				return delivery.ProjectBudget{}, failure
			}
			return failure
		}},
		{"missing Human-kind source", func(h harness) error { h.m.principals.ActiveHumanByID = nil; return ErrUnavailable }},
		{"membership read failed", func(h harness) error {
			h.m.members = loopFaultMembers{ActiveMemberChecker: h.m.members, fail: failure}
			return failure
		}},
		{"membership revoked", func(h harness) error {
			h.m.members = loopFaultMembers{ActiveMemberChecker: h.m.members}
			return ErrForbidden
		}},
		{"principal kind source failed", func(h harness) error {
			h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, false, failure
			}
			return failure
		}},
		{"missing Agent directory", func(h harness) error { h.m.agents.ByPrincipalOn = nil; return ErrUnavailable }},
		{"Agent directory failed", func(h harness) error {
			h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{}, false, failure
			}
			return failure
		}},
		{"Agent retired", func(h harness) error {
			h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{PrincipalID: 7, State: "RETIRED"}, true, nil
			}
			return ErrForbidden
		}},
		{"missing execution source", func(h harness) error { h.m.loopRuntime.executions.ReadOn = nil; return ErrUnavailable }},
		{"execution read failed", func(h harness) error {
			h.m.loopRuntime.executions.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]execution.LoopExecutionFact, error) {
				return nil, failure
			}
			return failure
		}},
		{"assignment read failed", func(h harness) error {
			h.m.loopRuntime.assignments.AgentsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (map[guid.ID]guid.ID, error) {
				return nil, failure
			}
			return failure
		}},
		{"settlement read failed", func(h harness) error {
			h.m.loopRuntime.settlements.ExecutionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) {
				return 0, false, failure
			}
			return failure
		}},
		{"missing audit", func(h harness) error { h.m.audit = nil; return ErrUnavailable }},
		{"missing publisher", func(h harness) error { h.m.events = nil; return ErrUnavailable }},
		{"pause scheduler unavailable", func(h harness) error { h.m.loopReminders.ChangeOn = nil; return ErrUnavailable }},
		{"pause scheduler failed", func(h harness) error {
			h.m.loopReminders.ChangeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return failure }
			return failure
		}},
		{"missing notifier", func(h harness) error { h.m.reportNotifier = nil; return ErrUnavailable }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, contract, run, in := operationalLoopFixture(t)
			wanted := tc.mutate(h)
			var before int64
			require.NoError(t, h.db.Table("audit_probe").Count(&before).Error)
			e := h.db.Transaction(func(tx *gorm.DB) error {
				c, r, e := h.m.authorizeLoopResultOn(claims(t, 7), tx, 1, run.ID)
				if e != nil {
					return e
				}
				_, e = h.m.completeLoopOn(claims(t, 7), tx, c, r, in)
				return e
			})
			require.ErrorIs(t, e, wanted)
			var persisted model.LoopRun
			require.NoError(t, h.db.Table(h.m.names.LoopRuns.String()).Where("id=?", run.ID).Take(&persisted).Error)
			require.Equal(t, "OPEN", persisted.State)
			require.Nil(t, persisted.CompletedAt)
			c, e := h.m.loopOn(t.Context(), h.db, 1, contract.ID)
			require.NoError(t, e)
			require.Equal(t, "ACTIVE", c.State)
			require.Equal(t, contract.Version, c.Version)
			var after int64
			require.NoError(t, h.db.Table("audit_probe").Count(&after).Error)
			require.Equal(t, before, after)
			require.NoError(t, h.db.Table("scheduler_probe").Where("state=?", "PAUSED").Count(&after).Error)
			require.Zero(t, after)
		})
	}
}
func TestLoopOutboxAndOwnerNoticeFailureRollBackPauseAndResult(t *testing.T) {
	for _, kind := range []string{"outbox", "planning.loop_escalated", "planning.loop_paused"} {
		t.Run(kind, func(t *testing.T) {
			h, contract, run, in := operationalLoopFixture(t)
			if kind == "outbox" {
				h.m.events = failingPlanningPublisher{}
			} else {
				h.m.reportNotifier = loopFaultNotifier{Notifier: h.m.reportNotifier, failType: kind}
			}
			require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
				c, r, e := h.m.authorizeLoopResultOn(claims(t, 7), tx, 1, run.ID)
				if e != nil {
					return e
				}
				_, e = h.m.completeLoopOn(claims(t, 7), tx, c, r, in)
				return e
			}))
			var state string
			require.NoError(t, h.db.Table(h.m.names.LoopRuns.String()).Select("state").Where("id=?", run.ID).Scan(&state).Error)
			require.Equal(t, "OPEN", state)
			c, e := h.m.loopOn(t.Context(), h.db, 1, contract.ID)
			require.NoError(t, e)
			require.Equal(t, "ACTIVE", c.State)
			require.Zero(t, c.ConsecutiveFailures)
			var n int64
			require.NoError(t, h.db.Table("planning_notice_probe").Count(&n).Error)
			require.Zero(t, n)
			require.NoError(t, h.db.Table("scheduler_probe").Where("state=?", "PAUSED").Count(&n).Error)
			require.Zero(t, n)
		})
	}
}
func TestTimeoutSourceFailureNeverCommitsAResult(t *testing.T) {
	failure := errors.New("timeout source unavailable")
	cases := []struct {
		name   string
		mutate func(harness) error
	}{
		{"missing runtime", func(h harness) error { h.m.loopRuntime.executions.ReadOn = nil; return ErrUnavailable }},
		{"missing principal fence", func(h harness) error { h.m.principals.LockActiveRow = nil; return ErrUnavailable }},
		{"principal fence failed", func(h harness) error {
			h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, false, failure
			}
			return failure
		}},
		{"project fence failed", func(h harness) error {
			h.m.loopRuntime.projects.LockOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ProjectBudget, error) {
				return delivery.ProjectBudget{}, failure
			}
			return failure
		}},
		{"execution source failed", func(h harness) error {
			h.m.loopRuntime.executions.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]execution.LoopExecutionFact, error) {
				return nil, failure
			}
			return failure
		}},
		{"settlement source failed", func(h harness) error {
			h.m.loopRuntime.settlements.ExecutionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) {
				return 0, false, failure
			}
			return failure
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, contract, run, _ := operationalLoopFixture(t)
			at := h.m.now().Add(31 * time.Minute)
			h.m.now = func() time.Time { return at }
			wanted := tc.mutate(h)
			require.ErrorIs(t, h.m.scanLoopTimeouts(t.Context(), 1), wanted)
			var state string
			require.NoError(t, h.db.Table(h.m.names.LoopRuns.String()).Select("state").Where("id=?", run.ID).Scan(&state).Error)
			require.Equal(t, "OPEN", state)
			c, e := h.m.loopOn(t.Context(), h.db, 1, contract.ID)
			require.NoError(t, e)
			require.Equal(t, contract.Version, c.Version)
		})
	}
}
func TestSettledTimeoutUsesSystemAuthorityAndNeverClaimsSuccess(t *testing.T) {
	h, contract, run, _ := operationalLoopFixture(t)
	at := h.m.now().Add(31 * time.Minute)
	h.m.now = func() time.Time { return at }
	var actorKind string
	original := h.m.events
	h.m.events = loopEventProbe{Publisher: original, seen: func(event events.Event) {
		if event.Type == "loop_run.escalated" {
			actorKind = event.Actor.Kind
			require.Zero(t, event.Actor.ID)
			require.Equal(t, "scheduled:planning.loop_run_timeout", event.Actor.AuthoritySource)
		}
	}}
	require.NoError(t, h.m.loopTimeoutJobHandler().Run(t.Context(), jobs.Job{TenantID: 1}))
	require.Equal(t, "SYSTEM", actorKind)
	var row model.LoopRun
	require.NoError(t, h.db.Table(h.m.names.LoopRuns.String()).Where("id=?", run.ID).Take(&row).Error)
	require.Equal(t, "TIMED_OUT", row.State)
	require.EqualValues(t, 1, row.CostMicros)
	require.Nil(t, loopRunView(&row).CostMicros)
	c, e := h.m.loopOn(t.Context(), h.db, 1, contract.ID)
	require.NoError(t, e)
	require.Equal(t, "PAUSED", c.State)
	require.Equal(t, 1, c.ConsecutiveFailures)
}

type loopEventProbe struct {
	events.Publisher
	seen func(events.Event)
}

func (p loopEventProbe) Publish(ctx context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
	p.seen(event)
	return p.Publisher.Publish(ctx, tx, event)
}

type loopFaultMembers struct {
	core.ActiveMemberChecker
	fail error
}

func (m loopFaultMembers) IsMemberActiveOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
	return false, m.fail
}

func TestLoopNoticeCanonicalLinkGroupingAndCurrentAgentOwner(t *testing.T) {
	h, contract, run, in := operationalLoopFixture(t)
	original := h.m.agents.ByPrincipalOn
	h.m.agents.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		a, found, err := original(ctx, tx, tenant, id)
		a.OwnerPrincipalID = 3
		return a, found, err
	}
	sink := &reminderNotifier{}
	h.m.reportNotifier = sink
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		c, r, err := h.m.authorizeLoopResultOn(claims(t, 7), tx, 1, run.ID)
		if err != nil {
			return err
		}
		_, err = h.m.completeLoopOn(claims(t, 7), tx, c, r, in)
		return err
	}))
	require.Len(t, sink.notices, 2)
	require.Equal(t, "planning:loop-escalated:"+contract.ID.String(), sink.notices[0].DedupeKey)
	for _, n := range sink.notices {
		require.Equal(t, "/admin/1/agents?agent=7", n.LinkPath)
	}
	paused := sink.notices[1]
	require.Equal(t, "planning.loop_paused", paused.Type)
	require.Contains(t, paused.Recipients, guid.ID(3))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		yes, err := h.m.loopNotificationEligibleOn(t.Context(), tx, 1, run.ID, 3, "planning.loop_paused")
		require.NoError(t, err)
		require.True(t, yes)
		h.m.agents.ByPrincipalOn = original
		yes, err = h.m.loopNotificationEligibleOn(t.Context(), tx, 1, run.ID, 3, "planning.loop_paused")
		require.NoError(t, err)
		require.False(t, yes)
		return nil
	}))
}

func TestLoopCompletionValidatesFindingsAndRollsBackStorageFailure(t *testing.T) {
	for _, mode := range []string{"instant", "timeout", "check_failed", "long_note", "invalid_note", "many_findings", "bad_finding", "run_write", "contract_write"} {
		t.Run(mode, func(t *testing.T) {
			h := loopHarness(t)
			cost := int64(1)
			settled := true
			bindLoopFacts(h, &cost, &settled)
			c := createTestLoop(t, h, true)
			var run LoopRunView
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				var e error
				run, _, e = h.m.openLoopRunOn(t.Context(), tx, 1, c.ID, 1, h.m.now())
				return e
			}))
			in := &LoopResultInput{}
			body := `{"self_check_results":[{"key":"evidence","passed":true}],"check_result":{"status":"OK"}}`
			if mode == "check_failed" {
				body = `{"self_check_results":[{"key":"evidence","passed":true}],"check_result":{"status":"FAIL"}}`
			}
			if mode == "bad_finding" {
				body = `{"self_check_results":[{"key":"evidence","passed":true}],"check_result":{"status":"OK","findings":[{"title":"Actual finding","severity":"INVALID"}]}}`
			}
			if mode == "many_findings" {
				body = `{"self_check_results":[{"key":"evidence","passed":true}],"check_result":{"status":"OK","findings":[` + strings.TrimSuffix(strings.Repeat(`{"title":"Actual finding","severity":"WARN"},`, 51), ",") + `]}}`
			}
			require.NoError(t, json.Unmarshal([]byte(body), &in.Body))
			if mode == "long_note" {
				in.Body.Note = strings.Repeat("x", 2001)
			}
			if mode == "invalid_note" {
				in.Body.Note = string([]byte{255})
			}
			now := h.m.now()
			if mode == "timeout" {
				now = now.Add(31 * time.Minute)
			}
			h.m.now = func() time.Time { return now }
			if mode == "run_write" || mode == "contract_write" {
				table := h.m.names.LoopRuns.String()
				if mode == "contract_write" {
					table = h.m.names.LoopContracts.String()
				}
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("loop_complete_write", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			var out LoopRunView
			err := h.db.Transaction(func(tx *gorm.DB) error {
				contract, row, e := h.m.authorizeLoopResultOn(claims(t, 7), tx, 1, run.ID)
				if e != nil {
					return e
				}
				out, e = h.m.completeLoopOn(claims(t, 7), tx, contract, row, in)
				return e
			})
			require.NoError(t, h.db.Callback().Update().Remove("loop_complete_write"))
			var row model.LoopRun
			require.NoError(t, h.db.Table(h.m.names.LoopRuns.String()).Where("id=?", run.ID).First(&row).Error)
			switch mode {
			case "instant":
				require.NoError(t, err)
				require.Equal(t, "SUCCEEDED", out.State)
				require.True(t, out.CompletedAt.After(run.OpenedAt))
			case "timeout":
				require.NoError(t, err)
				require.Equal(t, "TIMED_OUT", out.State)
			case "check_failed":
				require.NoError(t, err)
				require.Contains(t, []string{"FAILED", "ESCALATED"}, out.State)
			default:
				require.Error(t, err)
				require.Equal(t, "OPEN", row.State)
			}
		})
	}
}
