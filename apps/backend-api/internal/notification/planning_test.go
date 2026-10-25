package notification

import (
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"testing"
	"time"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPlanningProjectionRechecksCurrentSourceAudienceReplayAndPrivacy(t *testing.T) {
	f := newFixture(t)
	state, allowed := "SUBMITTED", true
	f.m.planningFacts = PlanningNotifications{
		FactsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (PlanningNotificationFact, bool, error) {
			return PlanningNotificationFact{ProjectID: 9, State: state, LinkPath: "/admin/7/entities/9/plans?record_id=91&principal_id=11"}, true, nil
		},
		EligibleOn: func(_ context.Context, _ *gorm.DB, _, _, recipient guid.ID, _, _ string) (bool, error) {
			return allowed && recipient == 12, nil
		},
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, Type: "planning.changed", Data: json.RawMessage(`{"kind":"plan","action":"SUBMIT","reason":"private","notify_principal_ids":[11]}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	row := rows[0]
	require.EqualValues(t, 12, row.RecipientPrincipalID)
	require.Equal(t, "planning.plan_submitted", row.Type)
	require.Equal(t, 1, row.MergeCount)
	require.NotContains(t, row.Params, "private")
	require.Equal(t, "IMMEDIATE", defaultEmail(row.Type, Action))
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		text, err := view(i18n.WithLocale(t.Context(), locale), row)
		require.NoError(t, err)
		require.NotContains(t, text.Body, "private")
		require.Contains(t, text.Body, "91")
	}
	allowed = false
	yes, err := f.m.visibleOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.False(t, yes)
	allowed = true
	state = "CONFIRMED"
	yes, err = f.m.visibleOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.False(t, yes)
}

func TestGeneratedReportUsesActualDraftFactRatherThanInventedBlockedState(t *testing.T) {
	f := newFixture(t)
	f.m.planningFacts = PlanningNotifications{
		FactsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (PlanningNotificationFact, bool, error) {
			return PlanningNotificationFact{ProjectID: 9, State: "DRAFT", LinkPath: "/admin/7/entities/9/reports?record_id=91"}, true, nil
		},
		EligibleOn: func(_ context.Context, _ *gorm.DB, _, _, id guid.ID, kind, state string) (bool, error) {
			require.Equal(t, "report", kind)
			require.Equal(t, "DRAFT", state)
			return id == 11, nil
		},
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, Type: "planning.report_generated", Data: json.RawMessage(`{}`)}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "planning.report_generated", rows[0].Type)
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		_, err := view(i18n.WithLocale(t.Context(), locale), rows[0])
		require.NoError(t, err)
	}
	allowed, err := f.m.sourceAudienceOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestGoalRiskProjectionUsesSourceDayAndCurrentAudience(t *testing.T) {
	f := newFixture(t)
	allowed := true
	f.m.planningFacts = PlanningNotifications{FactsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (PlanningNotificationFact, bool, error) {
		return PlanningNotificationFact{ProjectID: 9, State: "AT_RISK", DayKey: "2026-10-20", LinkPath: "/admin/7/entities/9/goals"}, true, nil
	}, EligibleOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, string) (bool, error) {
		return allowed, nil
	}}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, Type: "planning.changed", Data: []byte(`{"kind":"goal","action":"REFRESH","state":"AT_RISK","from_state":"ACTIVE"}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, r := range rows {
		require.Equal(t, 1, r.MergeCount)
		require.Contains(t, *r.DedupeSlot, "2026-10-20")
	}
	allowed = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], rows[0].RecipientPrincipalID)
	require.NoError(t, err)
	require.False(t, yes)
}

func TestSubmittedRecheckNoticeIsDailyAndDisappearsAfterAuthorRecheck(t *testing.T) {
	f := newFixture(t)
	stale := true
	f.m.planningFacts = PlanningNotifications{
		FactsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (PlanningNotificationFact, bool, error) {
			return PlanningNotificationFact{ProjectID: 9, State: "SUBMITTED", NeedsRecheck: stale, LinkPath: "/admin/7/entities/9/reports?record_id=91"}, true, nil
		},
		EligibleOn: func(_ context.Context, _ *gorm.DB, _, _, actor guid.ID, _, state string) (bool, error) {
			require.Equal(t, "NEEDS_RECHECK", state)
			return stale && actor == 11, nil
		},
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, Type: "planning.changed", Data: json.RawMessage(`{"kind":"report","action":"RECHECK","state":"SUBMITTED"}`)}
	for _, id := range []guid.ID{1001, 1002} {
		e.ID = id
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "planning.report_needs_recheck", rows[0].Type)
	stale = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, yes)
}

func TestPlanningNotificationConnectorRejectsInvalidAndDuplicateBinding(t *testing.T) {
	f := newFixture(t)
	s := module.NewServices()
	require.NoError(t, f.m.providePlanningNotifications(s))
	require.NoError(t, s.Seal())
	c, e := module.Resolve[PlanningNotificationConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, c.Bind(PlanningNotifications{}), ErrInvalid)
	facts := PlanningNotifications{FactsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (PlanningNotificationFact, bool, error) {
		return PlanningNotificationFact{}, false, nil
	}, EligibleOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, string) (bool, error) {
		return false, nil
	}}
	require.NoError(t, c.Bind(facts))
	require.ErrorIs(t, c.Bind(facts), ErrInvalid)
}
func TestPlanningProjectionFailuresNeverPersistNotification(t *testing.T) {
	for _, mode := range []string{"source", "missing", "candidates", "ordering", "eligible", "insert", "stale", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.planningFacts = PlanningNotifications{FactsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (PlanningNotificationFact, bool, error) {
				if mode == "source" {
					return PlanningNotificationFact{}, false, ErrUnavailable
				}
				state := "SUBMITTED"
				if mode == "stale" {
					state = "CONFIRMED"
				}
				return PlanningNotificationFact{ProjectID: 9, State: state, LinkPath: "/admin/7/plans"}, true, nil
			}, EligibleOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, string) (bool, error) {
				if mode == "eligible" {
					return false, ErrUnavailable
				}
				return true, nil
			}}
			f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) {
				if mode == "candidates" {
					return nil, ErrUnavailable
				}
				if mode == "ordering" {
					return []guid.ID{0}, nil
				}
				return []guid.ID{11}, nil
			}
			if mode == "missing" {
				f.m.planningFacts.FactsOn = nil
			}
			if mode == "insert" {
				f.ids.err = ErrUnavailable
			}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, Type: "planning.changed"}
			if mode == "invalid" {
				e.ID = 0
			}
			var data map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(`{"kind":"plan","action":"SUBMIT"}`), &data))
			err := f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectPlanningEventOn(t.Context(), tx, e, data) })
			if mode == "stale" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			n, x := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, x)
			require.Zero(t, n)
		})
	}
}
func TestPlanningDeadlineAudienceUsesCurrentDeadlineAndSourceState(t *testing.T) {
	f := newFixture(t)
	due := f.m.now().Add(-time.Hour)
	state := "SUBMITTED"
	f.m.planningFacts = PlanningNotifications{FactsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (PlanningNotificationFact, bool, error) {
		return PlanningNotificationFact{ProjectID: 9, State: state, LinkPath: "/plans", ReminderDueAt: &due}, true, nil
	}, EligibleOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, string) (bool, error) {
		return true, nil
	}}
	row := &model.Notification{TenantID: 7, ProjectID: 9, Type: "planning.plan_unconfirmed", Params: `{"subject_id":"91"}`, LinkPath: "/plans"}
	yes, e := f.m.planningAudienceOn(t.Context(), f.db, row, 11)
	require.NoError(t, e)
	require.True(t, yes)
	state = "CONFIRMED"
	yes, e = f.m.planningAudienceOn(t.Context(), f.db, row, 11)
	require.NoError(t, e)
	require.False(t, yes)
	row.Type = "planning.goal_close_overdue"
	state = "ACTIVE"
	yes, e = f.m.planningAudienceOn(t.Context(), f.db, row, 11)
	require.NoError(t, e)
	require.True(t, yes)
	state = "CLOSED"
	yes, e = f.m.planningAudienceOn(t.Context(), f.db, row, 11)
	require.NoError(t, e)
	require.False(t, yes)
	for _, params := range []string{"[", `{"subject_id":"bad"}`} {
		row.Params = params
		_, e = f.m.planningAudienceOn(t.Context(), f.db, row, 11)
		require.ErrorIs(t, e, ErrInvalid)
	}
}
