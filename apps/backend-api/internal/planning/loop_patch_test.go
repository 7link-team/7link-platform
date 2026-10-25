package planning

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestLoopEditsRespectImmutableBindingAndAtomicScheduler(t *testing.T) {
	h := loopHarness(t)
	v := createTestLoop(t, h, false)
	name := "Revised check"
	interval := 30
	overlap := true
	checks := []LoopSelfCheck{}
	budget := LoopBudget{MaxCostMicrosPerRun: 5000, MaxDurationMinutes: 60}
	escalation := LoopEscalation{OnRequiredCheckFailed: true, OnBudgetExceeded: true, ConsecutiveFailuresToPause: 2}
	anchor := LoopAnchor{ChannelID: 8, ThreadRootMessageID: 9}
	patch := LoopPatch{Name: &name, IntervalMinutes: &interval, AllowOverlap: &overlap, SelfChecks: &checks, Budget: &budget, Escalation: &escalation, Anchor: &anchor}
	var updated LoopContractView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		updated, e = h.m.patchLoopOn(claims(t, 2), tx, 1, v.ID, 1, patch)
		return e
	}))
	require.Equal(t, name, updated.Name)
	require.Equal(t, 30, *updated.IntervalMinutes)
	require.Empty(t, updated.SelfChecks)
	require.True(t, updated.AllowOverlap)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.patchLoopOn(claims(t, 2), tx, 1, v.ID, 1, patch); return e }), ErrConflict)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		updated, e = h.m.changeLoopStateOn(claims(t, 2), tx, 1, v.ID, updated.Version, "ACTIVE")
		return e
	}))
	h.m.loopReminders.UpsertOn = func(context.Context, *gorm.DB, guid.ID, agents.LoopReminderInput) (guid.ID, error) {
		return 0, errors.New("scheduler unavailable")
	}
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.patchLoopOn(claims(t, 2), tx, 1, v.ID, updated.Version, patch)
		return e
	}))
	h.m.loopReminders.UpsertOn = func(context.Context, *gorm.DB, guid.ID, agents.LoopReminderInput) (guid.ID, error) { return 999, nil }
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.patchLoopOn(claims(t, 2), tx, 1, v.ID, updated.Version, patch)
		return e
	}), ErrConflict)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.patchLoopOn(claims(t, 2), tx, 1, v.ID, updated.Version, LoopPatch{ReportBinding: &LoopReportBinding{Level: "MEMBER", PeriodKind: "DAY"}})
		return e
	}), ErrConflict)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, v.ID, updated.Version, "ARCHIVED")
		return e
	}))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.patchLoopOn(claims(t, 2), tx, 1, v.ID, updated.Version+1, patch)
		return e
	}), ErrConflict)
}
func TestLoopObserverRolloverPreservesCumulativeFireAndRollsBack(t *testing.T) {
	h := loopHarness(t)
	v := createTestLoop(t, h, true)
	// The successor is a distinct actual reminder; its first fire maps to1001.
	h.m.loopReminders.UpsertOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, in agents.LoopReminderInput) (guid.ID, error) {
		require.Zero(t, in.ReminderID)
		return 501, tx.Exec("INSERT INTO scheduler_probe VALUES (501,'ACTIVE')").Error
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		skip, e := h.m.openLoopReminderOn(t.Context(), tx, 1, 500, 1000, h.m.now())
		require.False(t, skip)
		return e
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		skip, e := h.m.openLoopReminderOn(t.Context(), tx, 1, 501, 1, h.m.now())
		require.True(t, skip)
		return e
	}))
	q := query.BindLoopRuns(h.db, h.m.names)
	runs, e := q.WithContext(t.Context()).Where(q.LoopContractID.Eq(v.ID)).Order(q.FireNo).Find()
	require.NoError(t, e)
	require.Len(t, runs, 2)
	require.Equal(t, 1000, runs[0].FireNo)
	require.Equal(t, 1001, runs[1].FireNo)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		skip, e := h.m.openLoopReminderOn(t.Context(), tx, 1, 999, 1, h.m.now())
		require.False(t, skip)
		return e
	}))
	// Callback failure must preserve existing contract/reminder binding.
	h.m.loopReminders.UpsertOn = nil
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.openLoopReminderOn(t.Context(), tx, 1, 501, 1000, h.m.now())
		return e
	}), ErrUnavailable)
	count, e := q.WithContext(t.Context()).Count()
	require.NoError(t, e)
	require.EqualValues(t, 2, count)
}

func TestLoopReportBindingAndScheduleEditsRoundTrip(t *testing.T) {
	h := loopHarness(t)
	definition := testLoopDefinition()
	definition.Kind = "REPORT"
	definition.OutputFormat = "REPORT_DRAFT"
	definition.ReportBinding = &LoopReportBinding{Level: "MEMBER", PeriodKind: "DAY"}
	var v LoopContractView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = h.m.createLoopOn(claims(t, 2), tx, 1, definition, false)
		return e
	}))
	periodKind := "WEEK"
	schedule := "PERIOD"
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = h.m.patchLoopOn(claims(t, 2), tx, 1, v.ID, v.Version, LoopPatch{ScheduleKind: &schedule, PeriodKind: &periodKind, ReportBinding: &LoopReportBinding{Level: "PROJECT", PeriodKind: "MONTH"}})
		return e
	}))
	require.Equal(t, "PERIOD", v.ScheduleKind)
	require.Nil(t, v.IntervalMinutes)
	require.Equal(t, "WEEK", *v.PeriodKind)
	require.Equal(t, "PROJECT", v.ReportBinding.Level)
	schedule = "INTERVAL"
	interval := 60
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = h.m.patchLoopOn(claims(t, 2), tx, 1, v.ID, v.Version, LoopPatch{ScheduleKind: &schedule, IntervalMinutes: &interval})
		return e
	}))
	require.Nil(t, v.PeriodKind)
	require.Equal(t, 60, *v.IntervalMinutes)
	require.Equal(t, "MONTH", v.ReportBinding.PeriodKind)
	bad := " "
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.patchLoopOn(claims(t, 2), tx, 1, v.ID, v.Version, LoopPatch{Name: &bad})
		return e
	}), ErrInvalid)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.patchLoopOn(claims(t, 2), tx, 1, 999, 1, LoopPatch{}); return e }), ErrNotFound)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.patchLoopOn(claims(t, 7), tx, 1, v.ID, v.Version, LoopPatch{})
		return e
	}), ErrForbidden)
}

func TestLoopEditCASAndAuditFailuresRollbackSchedulerAndContract(t *testing.T) {
	for _, mode := range []string{"cas", "audit", "missing-scheduler"} {
		t.Run(mode, func(t *testing.T) {
			h := loopHarness(t)
			v := createTestLoop(t, h, true)
			name := "Must roll back"
			failure := errors.New("audit failed")
			switch mode {
			case "cas":
				h.m.loopReminders.UpsertOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, in agents.LoopReminderInput) (guid.ID, error) {
					require.NoError(t, tx.Exec("INSERT INTO scheduler_probe VALUES (500,'EDIT')").Error)
					q := query.BindLoopContracts(tx, h.m.names)
					_, e := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.Version, v.Version+1)
					return in.ReminderID, e
				}
			case "audit":
				h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "missing-scheduler":
				h.m.loopReminders.UpsertOn = nil
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.patchLoopOn(claims(t, 2), tx, 1, v.ID, v.Version, LoopPatch{Name: &name})
				return e
			})
			switch mode {
			case "cas":
				require.ErrorIs(t, err, ErrConflict)
			case "audit":
				require.ErrorIs(t, err, failure)
			default:
				require.ErrorIs(t, err, ErrUnavailable)
			}
			row, e := h.m.loopOn(t.Context(), h.db, 1, v.ID)
			require.NoError(t, e)
			require.Equal(t, v.Name, row.Name)
			require.Equal(t, v.Version, row.Version)
			require.Equal(t, *v.ReminderID, *row.ReminderID)
			var n int64
			require.NoError(t, h.db.Table("scheduler_probe").Count(&n).Error)
			require.EqualValues(t, 1, n)
		})
	}
}

func TestLoopRolloverFailureCannotRebindOrConsumeFire(t *testing.T) {
	for _, mode := range []string{"same-id", "zero-id", "scheduler-error", "stale-version"} {
		t.Run(mode, func(t *testing.T) {
			h := loopHarness(t)
			v := createTestLoop(t, h, true)
			failure := errors.New("successor failed")
			h.m.loopReminders.UpsertOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, _ agents.LoopReminderInput) (guid.ID, error) {
				require.NoError(t, tx.Exec("INSERT INTO scheduler_probe VALUES (501,'SUCCESSOR')").Error)
				switch mode {
				case "same-id":
					return 500, nil
				case "zero-id":
					return 0, nil
				case "scheduler-error":
					return 0, failure
				default:
					q := query.BindLoopContracts(tx, h.m.names)
					_, e := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.Version, v.Version+1)
					return 501, e
				}
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.openLoopReminderOn(t.Context(), tx, 1, 500, 1000, h.m.now())
				return e
			})
			if mode == "scheduler-error" {
				require.ErrorIs(t, err, failure)
			} else {
				require.ErrorIs(t, err, ErrConflict)
			}
			row, e := h.m.loopOn(t.Context(), h.db, 1, v.ID)
			require.NoError(t, e)
			require.Equal(t, guid.ID(500), *row.ReminderID)
			require.Equal(t, v.Version, row.Version)
			require.Zero(t, row.ReminderFireOffset)
			q := query.BindLoopRuns(h.db, h.m.names)
			n, e := q.WithContext(t.Context()).Count()
			require.NoError(t, e)
			require.Zero(t, n)
			var count int64
			require.NoError(t, h.db.Table("scheduler_probe").Where("state = ?", "SUCCESSOR").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
