package planning

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/yunxiao"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type reportHistoryFixture struct {
	members
	since              time.Time
	intervals          []core.MembershipInterval
	observationMissing bool
	observationError   error
	intervalsError     error
}

func (h reportHistoryFixture) MembershipObservedSinceOn(context.Context, *gorm.DB, guid.ID) (*time.Time, error) {
	if h.observationMissing {
		return nil, h.observationError
	}
	return &h.since, h.observationError
}
func (h reportHistoryFixture) MembershipIntervalsOn(context.Context, *gorm.DB, guid.ID, time.Time, time.Time) ([]core.MembershipInterval, error) {
	return h.intervals, h.intervalsError
}

func generationHarness(t *testing.T) harness {
	h := setup(t)
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	h.m.members = reportHistoryFixture{since: since, intervals: []core.MembershipInterval{{PrincipalID: 2, Start: since}, {PrincipalID: 7, Start: since}}}
	h.m.now = func() time.Time { return time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC) }
	h.m.scheduledFacts = delivery.ScheduledPlanningFacts{ProjectsOn: func(_ context.Context, _ *gorm.DB, tenant guid.ID, _, _ time.Time) ([]guid.ID, error) {
		if tenant != 1 {
			return nil, ErrForbidden
		}
		return []guid.ID{3}, nil
	}, ReadOn: func(_ context.Context, _ *gorm.DB, tenant, project, subject guid.ID, _, _ time.Time) (delivery.PlanningSnapshot, error) {
		if tenant != 1 || project != 3 {
			return delivery.PlanningSnapshot{}, ErrForbidden
		}
		out := *h.facts
		out.Work = nil
		for _, work := range h.facts.Work {
			if subject == 0 || work.AccountablePrincipalID == subject {
				out.Work = append(out.Work, work)
			}
		}
		return out, nil
	}}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, PlanningSettingsPatch{RequiredMemberKinds: settingPtr([]string{"DAY", "WEEK"})})
		return err
	}))
	return h
}

func TestReportGenerationIsSystemScopedIdempotentAndAdoptable(t *testing.T) {
	h := generationHarness(t)
	require.ErrorIs(t, h.m.generateReports(claims(t, 3), 1), ErrForbidden)
	require.NoError(t, h.m.generateReports(t.Context(), 1))
	var rows []model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Order("id").Find(&rows).Error)
	require.Len(t, rows, 6, "DAY and WEEK each generate Human MEMBER, PROJECT and TEAM drafts")
	var aggregate model.Report
	for _, row := range rows {
		require.Zero(t, row.CreatedBy, "SYSTEM must not impersonate an owner")
		require.Equal(t, "DRAFT", row.State)
		require.NotNil(t, row.DueAt)
		if row.PrincipalID > 0 {
			require.Equal(t, guid.ID(2), row.PrincipalID)
			require.Zero(t, row.ProjectID)
		}
		var facts delivery.PlanningSnapshot
		require.NoError(t, json.Unmarshal([]byte(row.Facts), &facts))
		require.Contains(t, facts.MissingSources, "AGENT_MEMBERSHIP_HISTORY")
		if row.ProjectID == 3 && row.PeriodKind == "WEEK" {
			aggregate = row
		}
	}
	var before int64
	require.NoError(t, h.db.Table("audit_probe").Count(&before).Error)
	require.NoError(t, h.m.generateReports(t.Context(), 1))
	var count int64
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Count(&count).Error)
	require.EqualValues(t, 6, count)
	require.NoError(t, h.db.Table("audit_probe").Count(&count).Error)
	require.Equal(t, before, count, "replay cannot duplicate audit")
	var body PlanningReportInput
	require.NoError(t, json.Unmarshal([]byte(aggregate.Content), &body))
	body.Retrospective = "Human review of actual period facts"
	var adopted PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		adopted, err = h.m.editReportOn(claims(t, 3), tx, 1, aggregate.ID, aggregate.Version, body)
		return err
	}))
	require.Equal(t, guid.ID(3), adopted.AuthorID)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.editReportOn(claims(t, 2), tx, 1, aggregate.ID, adopted.Version, body)
		return err
	}), ErrForbidden, "authored drafts cannot be hijacked")
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, _, err := h.m.reportOn(claims(t, 3), tx, 1, aggregate.ID, "report_read", false)
		return err
	}))
}

func TestReportGenerationRollsBackAuditAndOutboxAndHonorsHistoryCutoff(t *testing.T) {
	for _, failure := range []string{"audit", "outbox", "facts"} {
		t.Run(failure, func(t *testing.T) {
			h := generationHarness(t)
			problem := errors.New("generation dependency failed")
			switch failure {
			case "audit":
				h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return problem }
			case "outbox":
				h.m.events = failingPlanningPublisher{}
			case "facts":
				h.m.scheduledFacts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
					return delivery.PlanningSnapshot{}, problem
				}
			}
			require.Error(t, h.m.generateReports(t.Context(), 1))
			var count int64
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Count(&count).Error)
			require.Zero(t, count, "generation cannot persist partial drafts on dependency failure")
		})
	}
	h := generationHarness(t)
	history := h.m.members.(reportHistoryFixture)
	history.since = h.m.now()
	h.m.members = history
	require.NoError(t, h.m.generateReports(t.Context(), 1))
	var count int64
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Count(&count).Error)
	require.Zero(t, count, "unknown pre-observation periods are never inferred")
	h.m.members = members{}
	require.ErrorIs(t, h.m.generateReports(t.Context(), 1), ErrUnavailable)
}

func TestReportGenerationScheduleRespectsLocalBoundaries(t *testing.T) {
	settings := defaultPlanningSettings()
	loc, err := time.LoadLocation(settings.Timezone)
	require.NoError(t, err)
	for _, tc := range []struct {
		kind, now, date string
		ready           bool
	}{
		{"DAY", "2026-10-26 17:59", "", false},
		{"DAY", "2026-10-26 18:00", "2026-10-26", true},
		{"DAY", "2026-10-25 20:00", "", false},
		{"WEEK", "2026-10-26 08:59", "", false},
		{"WEEK", "2026-10-26 09:00", "2026-10-19", true},
		{"MONTH", "2026-11-01 09:00", "2026-10-01", true},
		{"QUARTER", "2027-01-01 09:00", "2026-10-01", true},
		{"YEAR", "2027-01-01 09:00", "", false},
		{"YEAR", "2027-01-02 09:00", "2026-01-01", true},
	} {
		now, err := time.ParseInLocation("2006-01-02 15:04", tc.now, loc)
		require.NoError(t, err)
		date, ready := scheduledReportPeriod(tc.kind, settings, now)
		require.Equal(t, tc.ready, ready, tc.kind+tc.now)
		require.Equal(t, tc.date, date, tc.kind+tc.now)
	}
}

func TestReportEligibilityUsesActualLocalWorkdayOverlap(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	local := func(value string) time.Time {
		at, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
		require.NoError(t, err)
		return at
	}
	start, end := local("2026-10-19 00:00"), local("2026-10-26 00:00")
	cases := []struct {
		name, admitted, left, now string
		eligible                  bool
	}{
		{"whole observed week", "2026-10-18 00:00", "", "2026-10-26 00:00", true},
		{"weekend only", "2026-10-24 00:00", "2026-10-25 23:00", "2026-10-26 00:00", false},
		{"ends at first workday boundary", "2026-10-18 00:00", "2026-10-19 00:00", "2026-10-26 00:00", false},
		{"brief observed working day", "2026-10-19 09:00", "2026-10-19 09:01", "2026-10-26 00:00", true},
		{"future activation", "2026-10-23 09:00", "", "2026-10-22 18:00", false},
		{"admission at period end", "2026-10-26 00:00", "", "2026-10-27 00:00", false},
		{"left before period", "2026-10-01 00:00", "2026-10-18 23:59", "2026-10-26 00:00", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			interval := core.MembershipInterval{PrincipalID: 2, Start: local(tc.admitted)}
			if tc.left != "" {
				at := local(tc.left)
				interval.End = &at
			}
			require.Equal(t, tc.eligible, membershipOverlapsWorkday(interval, start, end, local(tc.now), loc, []int{1, 2, 3, 4, 5}))
		})
	}
}

func TestReportGenerationFailsAtomicallyWhenEligibilityDependenciesFail(t *testing.T) {
	problem := errors.New("eligibility source unavailable")
	for _, name := range []string{"observation", "intervals", "projects", "human", "lock", "identifier", "identifier_zero"} {
		t.Run(name, func(t *testing.T) {
			h := generationHarness(t)
			history := h.m.members.(reportHistoryFixture)
			expected := problem
			switch name {
			case "observation":
				history.observationError = problem
			case "intervals":
				history.intervalsError = problem
			case "projects":
				h.m.scheduledFacts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID, time.Time, time.Time) ([]guid.ID, error) { return nil, problem }
			case "human":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, problem
				}
			case "lock":
				h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, problem
				}
			case "identifier":
				h.m.ids = failingPlanningIDs{err: problem}
			case "identifier_zero":
				h.m.ids = failingPlanningIDs{}
				expected = ErrUnavailable
			}
			h.m.members = history
			require.ErrorIs(t, h.m.generateReports(t.Context(), 1), expected)
			var count int64
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Count(&count).Error)
			require.Zero(t, count, "failed historical eligibility cannot leave partial reports")
		})
	}
	h := generationHarness(t)
	require.ErrorIs(t, h.m.generateReports(t.Context(), 0), ErrInvalid)
	history := h.m.members.(reportHistoryFixture)
	history.observationMissing = true
	h.m.members = history
	require.NoError(t, h.m.generateReports(t.Context(), 1))
	var count int64
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Count(&count).Error)
	require.Zero(t, count, "unknown observation start cannot imply historical eligibility")
}

func TestReportGenerationKeepsUnassignedMembersAndDeduplicatesEligibility(t *testing.T) {
	h := generationHarness(t)
	history := h.m.members.(reportHistoryFixture)
	history.intervals = append(history.intervals, history.intervals[0], core.MembershipInterval{PrincipalID: 8, Start: history.since})
	h.m.members = history
	h.m.scheduledFacts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID, time.Time, time.Time) ([]guid.ID, error) {
		return []guid.ID{3, 4}, nil
	}
	h.m.scheduledFacts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
		return delivery.PlanningSnapshot{Work: []delivery.PlanningWorkFact{}}, nil
	}
	require.NoError(t, h.m.generateReports(t.Context(), 1))
	var rows []model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("period_kind=?", "WEEK").Find(&rows).Error)
	require.Len(t, rows, 4, "one eligible Human, two actual projects and team; duplicate observations and inactive principal excluded")
	for _, r := range rows {
		var body PlanningReportInput
		require.NoError(t, json.Unmarshal([]byte(r.Content), &body))
		if r.ProjectID == 0 && r.PrincipalID == 0 {
			require.Len(t, body.Children, 3, "unassigned member remains explicit alongside project drafts")
		} else {
			require.Empty(t, body.Children)
		}
	}
}

func TestNextScheduledReportAtIsStrictlyFutureAndUsesLocalCalendar(t *testing.T) {
	settings := defaultPlanningSettings()
	settings.Timezone = "America/Los_Angeles"
	loc, err := time.LoadLocation(settings.Timezone)
	require.NoError(t, err)
	parse := func(value string) time.Time {
		at, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
		require.NoError(t, err)
		return at
	}
	for _, tc := range []struct{ kind, now, want string }{
		{"DAY", "2026-10-23 17:59", "2026-10-23 18:00"},
		{"DAY", "2026-10-23 18:00", "2026-10-26 18:00"},
		{"DAY", "2026-10-25 12:00", "2026-10-26 18:00"},
		{"DAY", "2026-10-30 18:00", "2026-11-02 18:00"},
		{"WEEK", "2026-10-26 09:00", "2026-11-02 09:00"},
		{"MONTH", "2027-01-31 12:00", "2027-02-01 09:00"},
		{"QUARTER", "2026-12-31 12:00", "2027-01-01 09:00"},
		{"YEAR", "2027-01-02 09:00", "2028-01-02 09:00"},
	} {
		t.Run(tc.kind+tc.now, func(t *testing.T) {
			now := parse(tc.now)
			next, ok := nextScheduledReportAt(tc.kind, settings, now)
			require.True(t, ok)
			require.Equal(t, parse(tc.want), next)
			require.True(t, next.After(now))
		})
	}
	_, ok := nextScheduledReportAt("UNKNOWN", settings, parse("2026-10-23 18:00"))
	require.False(t, ok)
	settings.Workdays = nil
	_, ok = nextScheduledReportAt("DAY", settings, parse("2026-10-23 18:00"))
	require.False(t, ok)
	settings.Timezone = "Invalid/Zone"
	_, ok = nextScheduledReportAt("DAY", settings, time.Now())
	require.False(t, ok)
}

func TestReportGenerationRollsBackEarlierScopesWhenAggregateFactsFail(t *testing.T) {
	for _, failAt := range []int{2, 3} {
		t.Run(map[int]string{2: "project", 3: "team"}[failAt], func(t *testing.T) {
			h := generationHarness(t)
			reader := h.m.scheduledFacts.ReadOn
			calls := 0
			problem := errors.New("aggregate fact source lost")
			h.m.scheduledFacts.ReadOn = func(ctx context.Context, tx *gorm.DB, tenant, project, subject guid.ID, start, end time.Time) (delivery.PlanningSnapshot, error) {
				calls++
				if calls == failAt {
					return delivery.PlanningSnapshot{}, problem
				}
				return reader(ctx, tx, tenant, project, subject, start, end)
			}
			require.ErrorIs(t, h.m.generateReports(t.Context(), 1), problem)
			require.Equal(t, failAt, calls)
			var count int64
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Count(&count).Error)
			require.Zero(t, count, "a later aggregate failure rolls back earlier successfully inserted scopes")
			require.NoError(t, h.db.Table("audit_probe").Where("event_type=?", "planning.report_generated").Count(&count).Error)
			require.Zero(t, count, "earlier generation audit records share the failed transaction")
		})
	}
}

func TestReportGenerationRevalidatesMemberUnderPrincipalLock(t *testing.T) {
	h := generationHarness(t)
	h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	require.NoError(t, h.m.generateReports(t.Context(), 1))
	var members, aggregates int64
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("principal_id > 0").Count(&members).Error)
	require.Zero(t, members, "a member deactivated after eligibility selection must not receive a draft")
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("principal_id = 0").Count(&aggregates).Error)
	require.EqualValues(t, 4, aggregates, "independent project/team generation is retained")
}

func TestReportGenerationDeduplicatesFactsAndPreservesMissingProvenance(t *testing.T) {
	h := generationHarness(t)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	accepted := start.Add(time.Hour)
	h.m.scheduledFacts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
		return delivery.PlanningSnapshot{MissingSources: []string{"EXTERNAL_FACTS", "EXTERNAL_FACTS"}, Work: []delivery.PlanningWorkFact{{ID: 100, AccountablePrincipalID: 2, State: "ACCEPTED", AcceptedAt: &accepted}}}, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.generateReportOn(t.Context(), tx, 1, 0, 2, "WEEK", "2026-10-19", defaultPlanningSettings(), []guid.ID{3, 4}, start, end)
	}))
	var row model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).First(&row).Error)
	var body PlanningReportInput
	var facts delivery.PlanningSnapshot
	require.NoError(t, json.Unmarshal([]byte(row.Content), &body))
	require.NoError(t, json.Unmarshal([]byte(row.Facts), &facts))
	require.Len(t, facts.Work, 1, "cross-project repeated evidence is one fact")
	require.Len(t, body.Completed, 1, "cross-project repeated evidence is one completion")
	require.Equal(t, guid.ID(100), body.Completed[0].WorkItemID)
	require.Equal(t, []string{"AGENT_MEMBERSHIP_HISTORY", "HISTORICAL_PRINCIPAL_KIND", "SYSTEM_TEMPLATE_DRAFT", "EXTERNAL_FACTS"}, facts.MissingSources)
}

func TestReportScheduleRejectsMalformedStoredConfiguration(t *testing.T) {
	for _, tc := range []struct{ kind, value string }{
		{"WEEK", "MON"}, {"WEEK", "BAD 09:00"},
		{"MONTH", "D1"}, {"MONTH", "Dno 09:00"},
		{"DAY", "no-clock"}, {"DAY", "hour:00"}, {"DAY", "09:minute"},
	} {
		t.Run(tc.kind+tc.value, func(t *testing.T) {
			settings := defaultPlanningSettings()
			switch tc.kind {
			case "WEEK":
				settings.WeekReportAt = tc.value
			case "MONTH":
				settings.MonthReportAt = tc.value
			default:
				settings.DayReportAt = tc.value
			}
			now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
			due, anchor, ok := scheduledReportDue(tc.kind, settings, now)
			require.False(t, ok)
			require.True(t, due.IsZero())
			require.True(t, anchor.IsZero())
			next, ok := nextScheduledReportAt(tc.kind, settings, now)
			require.False(t, ok)
			require.True(t, next.IsZero(), "invalid stored settings cannot invent a timer")
		})
	}
}

func TestReportGenerationUnavailableSettingsCannotLeaveDrafts(t *testing.T) {
	h := generationHarness(t)
	require.NoError(t, h.db.Table(h.m.names.Settings.String()).Where("tenant_id=?", 1).Update("timezone", "Invalid/Zone").Error)
	require.ErrorIs(t, h.m.generateReports(t.Context(), 1), ErrUnavailable)
	var count int64
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Count(&count).Error)
	require.Zero(t, count)
	settings := defaultPlanningSettings()
	settings.Timezone = "Invalid/Zone"
	_, _, ok := scheduledReportDue("DAY", settings, h.m.now())
	require.False(t, ok)
}

func TestReportGenerationRollsBackWhenAggregateStorageFails(t *testing.T) {
	for _, stage := range []string{"project_children", "team_children", "unassigned_children", "insert"} {
		t.Run(stage, func(t *testing.T) {
			h := generationHarness(t)
			problem := errors.New("aggregate storage unavailable")
			queries := 0
			failQuery := 2 // First report query checks the unique report key.
			if stage == "unassigned_children" {
				failQuery = 3
			}
			require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("test:aggregate_storage", func(tx *gorm.DB) {
				if tx.Statement.Table == h.m.names.Reports.String() {
					queries++
					if stage != "insert" && queries == failQuery {
						tx.AddError(problem)
					}
				}
			}))
			require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("test:aggregate_insert", func(tx *gorm.DB) {
				if stage == "insert" && tx.Statement.Table == h.m.names.Reports.String() {
					tx.AddError(problem)
				}
			}))
			start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
			project := guid.ID(0)
			if stage == "project_children" {
				project = 3
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.generateReportOn(t.Context(), tx, 1, project, 0, "WEEK", "2026-10-19", defaultPlanningSettings(), []guid.ID{3}, start, start.AddDate(0, 0, 7))
			})
			require.ErrorIs(t, err, problem)
			require.NoError(t, h.db.Callback().Query().Remove("test:aggregate_storage"))
			require.NoError(t, h.db.Callback().Create().Remove("test:aggregate_insert"))
			var count int64
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Count(&count).Error)
			require.Zero(t, count, "failed child enumeration or insertion cannot leave a partial aggregate")
			require.NoError(t, h.db.Table("audit_probe").Where("event_type=?", "planning.report_generated").Count(&count).Error)
			require.Zero(t, count, "failed generation cannot emit its audit event")
		})
	}
}

func TestReportDraftIncludesActualIterationCarryovers(t *testing.T) {
	h := generationHarness(t)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	h.m.planningCarryovers = yunxiao.PlanningCarryovers{ReadOn: func(_ context.Context, _ *gorm.DB, tenant, project, subject guid.ID, from, until time.Time) ([]yunxiao.PlanningCarryoverFact, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		require.Equal(t, guid.ID(2), subject)
		require.Equal(t, start, from)
		require.Equal(t, end, until)
		return []yunxiao.PlanningCarryoverFact{{WorkItemID: 101, FromIterationID: 200, CarriedAt: start.Add(time.Hour)}}, nil
	}}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.generateReportOn(t.Context(), tx, 1, 0, 2, "WEEK", "2026-10-19", defaultPlanningSettings(), []guid.ID{3}, start, end)
	}))
	var row model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).First(&row).Error)
	var body PlanningReportInput
	require.NoError(t, json.Unmarshal([]byte(row.Content), &body))
	require.Contains(t, body.Uncompleted, "CARRIED_OVER work_item_id=101 from_iteration_id=200")
	for _, completion := range body.Completed {
		require.NotEqual(t, guid.ID(101), completion.WorkItemID, "carryover never invents accepted evidence")
	}
}

func TestGeneratedReportCapturesBlockedAndReworkFactsAndRejectsFailedProviders(t *testing.T) {
	for _, mode := range []string{"success", "carryovers", "member_quality", "scope_read", "quality", "ids", "settings", "insert"} {
		t.Run(mode, func(t *testing.T) {
			h := generationHarness(t)
			start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
			end := start.AddDate(0, 0, 7)
			settings, e := h.m.readSettingsOn(t.Context(), h.db, 1)
			require.NoError(t, e)
			h.facts.Work = append(h.facts.Work, delivery.PlanningWorkFact{ID: 901, State: "BLOCKED", Title: "Dependency blocked", AccountablePrincipalID: 2}, delivery.PlanningWorkFact{ID: 902, State: "REWORK", Title: "Quality correction", AccountablePrincipalID: 2})
			subject := guid.ID(2)
			switch mode {
			case "carryovers":
				h.m.planningCarryovers.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) ([]yunxiao.PlanningCarryoverFact, error) {
					return nil, ErrUnavailable
				}
			case "member_quality":
				h.m.memberQuality = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (*delivery.ReportQualityMetrics, error) {
					return nil, ErrUnavailable
				}
			case "scope_read":
				h.m.memberQuality = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (*delivery.ReportQualityMetrics, error) {
					return &delivery.ReportQualityMetrics{}, nil
				}
				old := h.m.scheduledFacts.ReadOn
				h.m.scheduledFacts.ReadOn = func(ctx context.Context, tx *gorm.DB, a, b, c guid.ID, d, e time.Time) (delivery.PlanningSnapshot, error) {
					if c == 0 {
						return delivery.PlanningSnapshot{}, ErrUnavailable
					}
					return old(ctx, tx, a, b, c, d, e)
				}
			case "quality":
				subject = 0
				h.m.reportQuality = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (*delivery.ReportQualityMetrics, error) {
					return nil, ErrUnavailable
				}
			case "ids":
				h.m.ids = failingPlanningIDs{err: ErrUnavailable}
			case "settings":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("generated_report_settings", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Settings.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "insert":
				require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("generated_report_insert", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Reports.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.generateReportOn(t.Context(), tx, 1, 0, subject, "WEEK", "2026-10-19", settings, []guid.ID{3}, start, end)
			})
			require.NoError(t, h.db.Callback().Create().Remove("generated_report_insert"))
			require.NoError(t, h.db.Callback().Query().Remove("generated_report_settings"))
			var rows []model.Report
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Find(&rows).Error)
			if mode == "success" {
				require.NoError(t, err)
				require.Len(t, rows, 1)
				var body PlanningReportInput
				require.NoError(t, json.Unmarshal([]byte(rows[0].Content), &body))
				reasons := map[string]bool{}
				for _, i := range body.UncompletedItems {
					reasons[i.Reason] = true
				}
				require.True(t, reasons["BLOCKED_DEPENDENCY"])
				require.True(t, reasons["QUALITY_REWORK"])
			} else {
				require.Error(t, err)
				require.Empty(t, rows)
			}
		})
	}
}
