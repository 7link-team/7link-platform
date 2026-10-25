package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestWorkScheduleCASDatesTimezoneClearAndAtomicAudit(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	start := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	end := start.Add(25 * time.Hour)
	in := scheduleBody{Version: work.Version, Start: &start, End: &end, Timezone: "America/Los_Angeles", Reason: "Schedule the DST transition review"}
	command := func() (WorkView, error) {
		var out WorkView
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.scheduleWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, in)
			return e
		})
		return out, err
	}
	out, err := command()
	require.NoError(t, err)
	require.Equal(t, start, *out.ScheduledStart)
	require.Equal(t, end, *out.ScheduledEnd)
	require.Equal(t, in.Timezone, out.ScheduleTimezone)
	require.Equal(t, work.Version+1, out.Version)
	_, err = command()
	require.ErrorIs(t, err, ErrWorkVersion)
	in.Version = out.Version
	in.End = &start
	_, err = command()
	require.ErrorIs(t, err, ErrInvalidArgument)
	in.End = &end
	in.Timezone = "not/a/timezone"
	_, err = command()
	require.ErrorIs(t, err, ErrInvalidArgument)
	in.Timezone = "UTC"
	in.Start = nil
	_, err = command()
	require.ErrorIs(t, err, ErrInvalidArgument)
	in.End = nil
	auditFn := m.service.audit
	m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	_, err = command()
	require.Error(t, err)
	q := query.BindWorkItems(db, m.service.names)
	stored, err := q.WithContext(ctx).Where(q.ID.Eq(work.ID)).First()
	require.NoError(t, err)
	require.NotNil(t, stored.ScheduledStart)
	require.Equal(t, out.Version, stored.Version)
	m.service.audit = auditFn
	out, err = command()
	require.NoError(t, err)
	require.Nil(t, out.ScheduledStart)
	require.Nil(t, out.ScheduledEnd)
}
func TestWorkCalendarOverlapUnscheduledAuthorityAndAssignee(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	claimed := assignmentClaim(t, m, db, ctx, work, false)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	in := scheduleBody{Version: claimed.WorkItem.Version, Start: &start, End: &end, Timezone: "UTC", Reason: "Multiweek scheduled work"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.scheduleWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, in); return e }))
	queryIn := calendarInput{Start: start.AddDate(0, 0, 3), End: end.AddDate(0, 0, -3), Limit: 50}
	rows, total, err := m.calendarRowsOn(ctx, db, 1, 99, 11, &queryIn)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	require.Equal(t, "HUMAN", rows[0].AssigneeKind)
	queryIn.AssigneeKind = "AGENT"
	rows, total, err = m.calendarRowsOn(ctx, db, 1, 99, 11, &queryIn)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, rows)
	queryIn.AssigneeKind = ""
	queryIn.Unscheduled = true
	rows, total, err = m.calendarRowsOn(ctx, db, 1, 99, 11, &queryIn)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, rows)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	queryIn.Unscheduled = false
	rows, total, err = m.calendarRowsOn(ctx, db, 1, 99, 11, &queryIn)
	// A no-row IAM constraint follows the existing permission-safe list convention.
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, total)
}
