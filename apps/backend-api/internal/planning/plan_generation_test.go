package planning

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUnfinishedReportSeedsReceiveAndRetainServerIdentity(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	r := create(t, h, "report", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-19"})
	var body PlanningReportInput
	require.NoError(t, json.Unmarshal(r.Content, &body))
	body.UncompletedItems = []PlanningUncompleted{{Key: "actual", Reason: "OTHER", Detail: "Still pending", CarryOver: "CARRY_TO_NEXT", Item: linkedItem(g)}}
	for range 2 {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			r, err = h.m.editReportOn(claims(t, 2), tx, 1, r.ID, r.Version, body)
			return err
		}))
		var saved PlanningReportInput
		require.NoError(t, json.Unmarshal(r.Content, &saved))
		require.Positive(t, int64(saved.UncompletedItems[0].Item.ID))
		if body.UncompletedItems[0].Item.ID > 0 {
			require.Equal(t, body.UncompletedItems[0].Item.ID, saved.UncompletedItems[0].Item.ID)
		}
		body = saved
	}
	body.UncompletedItems[0].Item.ID = 999
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.editReportOn(claims(t, 2), tx, 1, r.ID, r.Version, body)
		return err
	}), ErrInvalid)
}

func TestScheduledPlanCalendarUsesLocalEightAndPeriodAnchors(t *testing.T) {
	s := defaultPlanningSettings()
	s.Timezone = "America/Los_Angeles"
	loc, err := time.LoadLocation(s.Timezone)
	require.NoError(t, err)
	for _, tc := range []struct {
		kind, at, want string
		ready          bool
	}{
		{"DAY", "2026-10-26 07:59", "2026-10-26", false},
		{"DAY", "2026-10-26 08:00", "2026-10-26", true},
		{"DAY", "2026-10-25 08:00", "", false},
		{"WEEK", "2026-10-26 07:59", "2026-10-26", false},
		{"WEEK", "2026-10-26 08:00", "2026-10-26", true},
		{"WEEK", "2026-10-28 10:00", "2026-10-26", true},
		{"MONTH", "2026-11-01 08:00", "2026-11-01", true},
		{"QUARTER", "2027-01-01 08:00", "2027-01-01", true},
		{"YEAR", "2027-01-01 08:00", "2027-01-01", true},
	} {
		at, err := time.ParseInLocation("2006-01-02 15:04", tc.at, loc)
		require.NoError(t, err)
		date, ready := scheduledPlanPeriod(tc.kind, s, at)
		require.Equal(t, tc.want, date, tc.kind+tc.at)
		require.Equal(t, tc.ready, ready, tc.kind+tc.at)
	}
}

func TestScheduledPlanCarriesTypedProvenanceAndSeedsActualAssignedWork(t *testing.T) {
	h := generationHarness(t)
	g := goalTree(t, h)
	work := guid.ID(101)
	other := guid.ID(102)
	previous := PlanningReportInput{Uncompleted: "work_item_id=9999 never parse prose", UncompletedItems: []PlanningUncompleted{
		{Key: "continued", Reason: "OTHER", Detail: "Still assigned", CarryOver: "CARRY_TO_NEXT", Item: PlanningPlanItem{ID: 444, Title: "Continue actual task", GoalID: g.ID, KR: "delivery", WorkItemID: &work}},
		{Key: "drop", Reason: "OTHER", Detail: "Stopped", CarryOver: "DROP", Item: PlanningPlanItem{Title: "Do not carry", WorkItemID: &other}},
	}}
	body, hash, err := hashJSON(previous)
	require.NoError(t, err)
	row := model.Report{TenantID: 1, ID: 55, ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-19", State: "SUBMITTED", Content: body, ContentHash: hash, Facts: "{}", Version: 1, CreatedBy: 2}
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Create(&row).Error)
	h.facts.Work = append(h.facts.Work, delivery.PlanningWorkFact{ID: 102, Title: "New assigned task", State: "READY", AccountablePrincipalID: 2}, delivery.PlanningWorkFact{ID: 103, Title: "Other member", State: "IN_PROGRESS", AccountablePrincipalID: 3})
	require.ErrorIs(t, h.m.generatePlans(claims(t, 2), 1), ErrForbidden)
	require.NoError(t, h.m.generatePlans(t.Context(), 1))
	var plans []model.Plan
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Order("id").Find(&plans).Error)
	require.Len(t, plans, 10, "all five periods for the actual Human and active Agent roster")
	var target model.Plan
	for _, plan := range plans {
		require.Zero(t, plan.CreatedBy)
		require.Zero(t, plan.ProjectID)
		require.Equal(t, "DRAFT", plan.State)
		if plan.PrincipalID == 2 && plan.PeriodKind == "WEEK" {
			target = plan
		}
	}
	var draft PlanningPlanInput
	require.NoError(t, json.Unmarshal([]byte(target.Content), &draft))
	require.Len(t, draft.Items, 2)
	require.NotNil(t, draft.Items[0].CarriedFromItemID)
	require.Equal(t, guid.ID(444), *draft.Items[0].CarriedFromItemID)
	require.Positive(t, int64(draft.Items[0].ID))
	require.NotEqual(t, guid.ID(444), draft.Items[0].ID)
	require.Equal(t, &row.ID, draft.Items[0].CarriedFromReportID)
	require.Equal(t, "continued", draft.Items[0].CarriedFromKey)
	require.Equal(t, hash, draft.Items[0].CarriedFromHash)
	require.Equal(t, g.ID, draft.Items[0].GoalID)
	require.Equal(t, work, *draft.Items[0].WorkItemID)
	require.Equal(t, guid.ID(102), *draft.Items[1].WorkItemID)
	require.Zero(t, draft.Items[1].GoalID, "no invented goal association")
	require.NotEmpty(t, draft.Suggestions)
	var before int64
	require.NoError(t, h.db.Table("audit_probe").Count(&before).Error)
	require.NoError(t, h.m.generatePlans(t.Context(), 1))
	var after int64
	require.NoError(t, h.db.Table("audit_probe").Count(&after).Error)
	require.Equal(t, before, after)
	// Member explicitly links the new seed; normal tenant-plan submission uses
	// current project authorization for each item's actual goal.
	draft.Items[1].GoalID = g.ID
	draft.Items[1].KR = "delivery"
	var saved PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		saved, err = h.m.editPlanOn(claims(t, 2), tx, 1, target.ID, 1, target.ContentHash, draft)
		return err
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.decidePlanOn(claims(t, 2), tx, 1, saved.ID, PlanningDecisionInput{Version: saved.Version, ExpectedContentHash: saved.ContentHash, Action: "SUBMIT"})
		return err
	}))
	// Mutated source attribution fails closed, even while the work ID is real.
	forgedID := guid.ID(445)
	draft.Items[0].CarriedFromItemID = &forgedID
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.validatePlanItemsOn(claims(t, 2), tx, 1, 0, 2, draft.Items) }), ErrInvalid)
	sourceID := guid.ID(444)
	draft.Items[0].CarriedFromItemID = &sourceID
	draft.Items[0].CarriedFromHash = "forged"
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.validatePlanItemsOn(claims(t, 2), tx, 1, 0, 2, draft.Items) }), ErrConflict)
	draft.Items[0].CarriedFromHash = hash
	draft.Items[0].CarriedFromKey = "drop"
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.validatePlanItemsOn(claims(t, 2), tx, 1, 0, 2, draft.Items) }), ErrInvalid)
}

func TestScheduledPlanAtomicFailureAndCurrentEligibility(t *testing.T) {
	for _, failure := range []string{"audit", "outbox", "facts", "membership", "ids"} {
		t.Run(failure, func(t *testing.T) {
			h := generationHarness(t)
			problem := errors.New("dependency unavailable")
			switch failure {
			case "audit":
				h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return problem }
			case "outbox":
				h.m.events = failingPlanningPublisher{}
			case "facts":
				h.m.scheduledFacts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
					return delivery.PlanningSnapshot{}, problem
				}
			case "membership":
				history := h.m.members.(reportHistoryFixture)
				history.intervalsError = problem
				h.m.members = history
			case "ids":
				h.m.ids = failingPlanningIDs{err: problem}
			}
			require.Error(t, h.m.generatePlans(t.Context(), 1))
			var n int64
			require.NoError(t, h.db.Table(h.m.names.Plans.String()).Count(&n).Error)
			require.Zero(t, n)
			require.NoError(t, h.db.Table("audit_probe").Where("event_type=?", "planning.changed").Count(&n).Error)
			require.Zero(t, n)
		})
	}
	h := generationHarness(t)
	history := h.m.members.(reportHistoryFixture)
	left := h.m.now()
	history.intervals = []core.MembershipInterval{{PrincipalID: 2, Start: history.since, End: &left}, {PrincipalID: 9, Start: history.since}, {PrincipalID: 8, Start: history.since}, {PrincipalID: 3, Start: left.Add(time.Hour)}}
	h.m.members = history
	require.NoError(t, h.m.generatePlans(t.Context(), 1))
	var n int64
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Count(&n).Error)
	require.Zero(t, n)
}

func TestCarryOverIncludesBlockedAndAwaitingDecisionButNotAcceptedOrForeignWork(t *testing.T) {
	h := generationHarness(t)
	for _, state := range []string{"BLOCKED", "NEEDS_HUMAN_REVIEW", "ACCEPTED"} {
		t.Run(state, func(t *testing.T) {
			work := guid.ID(101)
			body, hash, err := hashJSON(PlanningReportInput{UncompletedItems: []PlanningUncompleted{{Key: "carry", Reason: "OTHER", Detail: "Await outcome", CarryOver: "CARRY_TO_NEXT", Item: PlanningPlanItem{Title: "Actual task", WorkItemID: &work}}}})
			require.NoError(t, err)
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", 55).Delete(&model.Report{}).Error)
			report := model.Report{TenantID: 1, ID: 55, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-19", State: "SUBMITTED", Content: body, ContentHash: hash, Facts: "{}", Version: 1}
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Create(&report).Error)
			h.facts.Work[1].State = state
			require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("principal_id=?", 2).Delete(&model.Plan{}).Error)
			require.NoError(t, h.m.generatePlans(t.Context(), 1))
			var row model.Plan
			require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("principal_id=? AND period_kind=?", 2, "WEEK").First(&row).Error)
			var result PlanningPlanInput
			require.NoError(t, json.Unmarshal([]byte(row.Content), &result))
			if state == "ACCEPTED" {
				require.Empty(t, result.Items)
			} else {
				require.Len(t, result.Items, 1)
				require.Equal(t, &work, result.Items[0].WorkItemID)
				require.NotNil(t, result.Items[0].CarriedFromReportID)
			}
		})
	}
}

func TestScheduledKRSuggestionsRequireCurrentContributionAndVisibility(t *testing.T) {
	h := generationHarness(t)
	g := goalTree(t, h)
	check := func(subject guid.ID) []PlanningPlanItem {
		t.Helper()
		var out []PlanningPlanItem
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = h.m.draftSuggestionsOn(t.Context(), tx, 1, subject, h.m.now(), h.m.now().Add(time.Hour), defaultPlanningSettings())
			return err
		}))
		return out
	}
	require.NotEmpty(t, check(2))
	require.Empty(t, check(3), "membership alone is not contribution")
	policy := h.m.entityPolicy
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	for _, item := range check(2) {
		require.NotEqual(t, g.ID, item.GoalID)
	}
	h.m.entityPolicy = policy
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("state", "CANCELLED").Error) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	for _, item := range check(2) {
		require.NotEqual(t, g.ID, item.GoalID)
	}
}

func TestReportN2ChecksCurrentAndNextPlansBeforeTimeout(t *testing.T) {
	h := setup(t)
	r := reportDraft(t, h)
	var report model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&report).Error)
	_, end, err := reportPeriod(&report)
	require.NoError(t, err)
	check := func(want bool) {
		t.Helper()
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			got, err := h.m.reportPlansConfirmedOn(t.Context(), tx, 1, &report, end)
			require.Equal(t, want, got)
			return err
		}))
	}
	check(false)
	for i, date := range []string{"2026-10-19", "2026-10-26"} {
		row := model.Plan{TenantID: 1, ID: guid.ID(200 + i), PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: date, State: "CONFIRMED", Content: "{}", ContentHash: "hash", Version: 1}
		require.NoError(t, h.db.Table(h.m.names.Plans.String()).Create(&row).Error)
		check(i == 1)
	}
	for _, state := range []string{"DRAFT", "SUBMITTED", "RETURNED"} {
		require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", 201).Update("state", state).Error)
		check(false)
	}
	// A project-specific pending plan cannot be hidden by a confirmed tenant plan.
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", 201).Update("state", "CONFIRMED").Error)
	row := model.Plan{TenantID: 1, ID: 202, ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-26", State: "SUBMITTED", Content: "{}", ContentHash: "hash", Version: 1}
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Create(&row).Error)
	check(false)
}

func TestGeneratedPlanSourcesAndPersistenceFailuresLeaveNoDraft(t *testing.T) {
	for _, fault := range []string{"plan_read", "goals_read", "reports_read", "projects", "period", "ids", "zero_id", "write", "prior_content", "prior_invalid"} {
		t.Run(fault, func(t *testing.T) {
			h := generationHarness(t)
			h.facts.Work = nil
			settings := defaultPlanningSettings()
			if fault == "projects" {
				h.m.scheduledFacts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID, time.Time, time.Time) ([]guid.ID, error) {
					return nil, ErrUnavailable
				}
			}
			if fault == "ids" {
				h.m.ids = failingPlanningIDs{err: ErrUnavailable}
			}
			if fault == "zero_id" {
				h.m.ids = failingPlanningIDs{}
			}
			if fault == "period" {
				settings.Timezone = "bad/zone"
			}
			if fault == "write" {
				require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("generated_plan_write_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Plans.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			if fault == "plan_read" || fault == "goals_read" || fault == "reports_read" {
				target := h.m.names.Plans.String()
				if fault == "goals_read" {
					target = h.m.names.Goals.String()
				}
				if fault == "reports_read" {
					target = h.m.names.Reports.String()
				}
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("generated_plan_read_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == target {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			if fault == "prior_content" || fault == "prior_invalid" {
				content := "{"
				if fault == "prior_invalid" {
					content = `{"uncompleted_items":[{"key":"invalid"}]}`
				}
				row := model.Report{TenantID: 1, ID: 55, ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-19", State: "SUBMITTED", Content: content, ContentHash: "prior", Facts: "{}", Version: 1, CreatedBy: 2}
				require.NoError(t, h.db.Table(h.m.names.Reports.String()).Create(&row).Error)
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.generatePlanOn(t.Context(), tx, 1, 2, "WEEK", "2026-10-26", settings)
			})
			require.Error(t, err)
			require.NoError(t, h.db.Callback().Query().Remove("generated_plan_read_failure"))
			require.NoError(t, h.db.Callback().Create().Remove("generated_plan_write_failure"))
			var count int64
			require.NoError(t, h.db.Table(h.m.names.Plans.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
func TestScheduledPlanGenerationRechecksPrincipalAndCalendarDependencies(t *testing.T) {
	for _, fault := range []string{"calendar", "lock", "human", "agent_capability", "agent_error", "database", "capability"} {
		t.Run(fault, func(t *testing.T) {
			h := generationHarness(t)
			switch fault {
			case "calendar":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("generation_calendar_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Settings.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "lock":
				h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "human":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "agent_capability":
				h.m.agents.ByPrincipalOn = nil
			case "agent_error":
				h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, ErrUnavailable
				}
			case "database":
				h.m.runtime = nil
			case "capability":
				h.m.ids = nil
			}
			require.Error(t, h.m.generatePlans(t.Context(), 1))
			require.NoError(t, h.db.Callback().Query().Remove("generation_calendar_failure"))
			var count int64
			require.NoError(t, h.db.Table(h.m.names.Plans.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
	h := generationHarness(t)
	require.ErrorIs(t, h.m.generatePlans(t.Context(), 0), ErrInvalid)
}

func TestDraftSuggestionsNeverInventUnreadableOrCompletedKeyResults(t *testing.T) {
	for _, mode := range []string{"missing_entity", "missing_tenant", "policy_failure", "hidden", "complete", "period", "content"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			g := goalTree(t, h)
			start := h.m.now().Add(-time.Hour)
			end := h.m.now().Add(time.Hour)
			switch mode {
			case "missing_entity":
				h.m.entityPolicy = nil
			case "missing_tenant":
				h.m.policy = nil
			case "policy_failure":
				h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrUnavailable
				}
			case "hidden":
				h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"99"}}, nil
				}
				h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "complete":
				var body PlanningGoalInput
				require.NoError(t, json.Unmarshal(g.Content, &body))
				body.KeyResults[0].Current = body.KeyResults[0].Target
				data, e := json.Marshal(body)
				require.NoError(t, e)
				require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("content", string(data)).Error)
			case "period":
				require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("period_start", "bad").Error)
			case "content":
				require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("content", "[").Error)
			}
			var items []PlanningPlanItem
			err := h.db.Transaction(func(tx *gorm.DB) error {
				var e error
				items, e = h.m.draftSuggestionsOn(t.Context(), tx, 1, 2, start, end, defaultPlanningSettings())
				return e
			})
			if mode == "hidden" {
				require.NoError(t, err)
				require.Empty(t, items)
			} else if mode == "complete" {
				require.NoError(t, err)
				for _, item := range items {
					require.NotEqual(t, g.ID, item.GoalID)
				}
			} else {
				require.Error(t, err)
			}
		})
	}
}
