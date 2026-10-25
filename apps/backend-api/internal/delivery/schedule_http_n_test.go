package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScheduleHTTPReplaysExactDatesAndCalendarDoesNotInventUnscheduledDates(t *testing.T) {
	m, _, ctx, work := assignmentHarness(t, true)
	start := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	end := start.Add(25 * time.Hour)
	in := &scheduleInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: work.ID.String()}, Key: "n-schedule-once", Body: scheduleBody{Version: work.Version, Start: &start, End: &end, Timezone: "America/Los_Angeles", Reason: "Explicit daylight savings interval"}}
	first, err := m.scheduleWorkHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, start, *first.Body.Data.ScheduledStart)
	replay, err := m.scheduleWorkHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, first.Body.Data.Version, replay.Body.Data.Version)
	page, err := m.calendarHTTP(ctx, &calendarInput{EntityID: "99", Start: start.Add(time.Hour), End: end.Add(-time.Hour), Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Body.Data, 1)
	require.Equal(t, work.ID, page.Body.Data[0].Work.ID)
	empty, err := m.calendarHTTP(ctx, &calendarInput{EntityID: "99", Unscheduled: true, Limit: 10})
	require.NoError(t, err)
	require.Empty(t, empty.Body.Data)
	_, err = m.calendarHTTP(ctx, &calendarInput{EntityID: "99", Limit: 10})
	require.Error(t, err, "a missing range cannot silently turn into today")
	in.Key = "n-stale-schedule"
	_, err = m.scheduleWorkHTTP(ctx, in)
	require.Error(t, err)
	in.WorkItemID = "bad"
	_, err = m.scheduleWorkHTTP(ctx, in)
	require.Error(t, err)
}

func TestScheduleCalendarFiltersActualExecutorAndFailsClosedOnQueryOutage(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	assignmentClaim(t, m, db, ctx, work, false)
	in := &calendarInput{EntityID: "99", Unscheduled: true, AssigneeKind: "HUMAN", AssigneeID: 11, Limit: 10}
	page, err := m.calendarHTTP(ctx, in)
	require.NoError(t, err)
	require.Len(t, page.Body.Data, 1)
	require.Equal(t, work.ID, page.Body.Data[0].Work.ID)
	require.Equal(t, guid.ID(11), *page.Body.Data[0].AssigneeID)
	in.AssigneeID = -1
	_, err = m.calendarHTTP(ctx, in)
	require.Error(t, err)
	in.AssigneeID = 11
	for _, fault := range []string{"assignment", "work", "policy", "owner_scope"} {
		t.Run(fault, func(t *testing.T) {
			failure := errors.New("calendar source unavailable")
			policy := m.workPolicy
			if fault == "policy" || fault == "owner_scope" {
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					if fault == "policy" {
						return authz.DataConstraint{}, failure
					}
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
			} else {
				table := m.service.names.Assignments.String()
				if fault == "work" {
					table = m.service.names.WorkItems.String()
				}
				require.NoError(t, m.runtime.Database().Write().Callback().Query().Before("gorm:query").Register("regression_calendar", func(tx *gorm.DB) {
					if strings.Contains(tx.Statement.Table, table) || (tx.Statement.TableExpr != nil && strings.Contains(tx.Statement.TableExpr.SQL, table)) {
						_ = tx.AddError(failure)
					}
				}))
			}
			out, err := m.calendarHTTP(ctx, in)
			require.Error(t, err)
			require.Nil(t, out, "query outage cannot be presented as an empty or partial successful calendar")
			m.workPolicy = policy
			if fault == "assignment" || fault == "work" {
				require.NoError(t, m.runtime.Database().Write().Callback().Query().Remove("regression_calendar"))
			}
		})
	}
}

func TestScheduleRejectsMissingReasonAndTerminalWorkWithoutMutation(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	in := scheduleBody{Version: work.Version, Timezone: "UTC", Reason: "Valid rescheduling reason"}
	for _, reason := range []string{"", "tiny"} {
		in.Reason = reason
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.scheduleWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, in)
			return err
		}), ErrInvalidArgument)
	}
	in.Reason = "Valid rescheduling reason"
	in.Timezone = " "
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.scheduleWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, in)
		return err
	}), ErrInvalidArgument)
	in.Timezone = "UTC"
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("state", "CANCELLED").Error) //nolint:misspell // Frozen persisted protocol state.
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.scheduleWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, in)
		return err
	}), ErrWorkPreparationState)
	var stored struct {
		Version        int
		ScheduledStart *time.Time
	}
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Take(&stored).Error)
	require.Equal(t, work.Version, stored.Version)
	require.Nil(t, stored.ScheduledStart)
}
