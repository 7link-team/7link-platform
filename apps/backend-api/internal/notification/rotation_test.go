package notification

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func rotationEvent(id guid.ID, retire time.Time, state string) events.RecordedEvent {
	data, _ := json.Marshal(map[string]any{"rotation_id": id, "state": state, "retire_old_at": retire})
	return events.RecordedEvent{ID: 1, TenantID: 7, SubjectID: 91, SubjectKind: "machine", Type: "machine.credential_changed", Data: data}
}
func warningJob() jobs.Job {
	return jobs.Job{TenantID: 7, Payload: json.RawMessage(`{"machine_id":"91","rotation_id":"77"}`)}
}

func TestRotationSchedulingRejectsInvalidFactsAndRollsBackOnQueueFailure(t *testing.T) {
	f := newFixture(t)
	e := rotationEvent(77, f.m.now(), "PENDING_DELIVERY")
	for _, payload := range []string{`{}`, `{"retire_old_at":"bad"}`, `{"retire_old_at":"2026-10-20T00:00:00Z","rotation_id":"0"}`} {
		var data map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(payload), &data))
		require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.scheduleRotationWarningOn(t.Context(), tx, e, data) }), ErrInvalid)
	}
	var data map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(e.Data, &data))
	sentinel := errors.New("admission denied")
	f.m.admission = func(context.Context, *gorm.DB, guid.ID) error { return sentinel }
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.scheduleRotationWarningOn(t.Context(), tx, e, data) }), sentinel)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.claimRotationAlertOn(t.Context(), tx, 7, 77, "GRACE")
		return err
	}), sentinel)
	f.m.admission = nil
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.scheduleRotationWarningOn(t.Context(), tx, e, data) }), ErrUnavailable)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.claimRotationAlertOn(t.Context(), tx, 7, 77, "GRACE")
		return err
	}), ErrUnavailable)
	_, err := f.m.claimRotationAlertOn(t.Context(), f.db, 0, 77, "GRACE")
	require.ErrorIs(t, err, ErrInvalid)
}
func TestRotationWarningScheduledAndSuppressedByCurrentFleetState(t *testing.T) {
	f := newFixture(t)
	retire := f.m.now().Add(24 * time.Hour)
	e := rotationEvent(77, retire, "PENDING_DELIVERY")
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	queue := f.m.queue.(*fixtureQueue)
	require.Len(t, queue.requests, 1)
	require.Equal(t, retire.Add(-2*time.Hour), queue.requests[0].RunAt)
	require.Equal(t, "rotation:77:grace", queue.requests[0].IdempotencyKey)
	require.Equal(t, Name+".rotation_warning", queue.requests[0].Kind)
	for _, state := range []string{"ACTIVE", "PENDING_DELIVERY", "FAILED", "UNKNOWN"} {
		f.m.rotationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.RotationNotificationFact, bool, error) {
			return fleet.RotationNotificationFact{State: state, RequestedBy: 11, RetireOldAt: retire}, true, nil
		}
		err := f.m.rotationWarningJob(t.Context(), warningJob())
		if state == "PENDING_DELIVERY" || state == "FAILED" {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
	q := query.BindRotationAlerts(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}
func TestRotationWarningEachConditionOnceAcrossReadAndTime(t *testing.T) {
	f := newFixture(t)
	retire := f.m.now().Add(time.Hour)
	f.m.rotationFacts = func(_ context.Context, _ *gorm.DB, tenant, machine, rotation guid.ID) (fleet.RotationNotificationFact, bool, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(91), machine)
		require.Equal(t, guid.ID(77), rotation)
		return fleet.RotationNotificationFact{State: "PENDING_DELIVERY", RequestedBy: 11, RetireOldAt: retire}, true, nil
	}
	require.NoError(t, f.m.rotationWarningJob(t.Context(), warningJob()))
	require.NoError(t, f.m.rotationWarningJob(t.Context(), warningJob()))
	e := rotationEvent(77, retire, "FAILED")
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 4)
	for _, r := range rows {
		v, err := view(t.Context(), r)
		require.NoError(t, err)
		require.Contains(t, v.Body, retire.Format(time.RFC3339))
		require.Contains(t, v.Body, "91")
	}
	now := f.m.now().Add(48 * time.Hour)
	f.m.now = func() time.Time { return now }
	_, err = q.WithContext(t.Context()).Where(q.TenantID.Eq(guid.ID(7))).UpdateSimple(q.ReadAt.Value(now))
	require.NoError(t, err)
	require.NoError(t, f.m.rotationWarningJob(t.Context(), warningJob()))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 4, count)
	alerts := query.BindRotationAlerts(f.db, f.m.names)
	count, err = alerts.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
}
func TestRotationWarningFailuresRollbackCondition(t *testing.T) {
	f := newFixture(t)
	sentinel := errors.New("fleet unavailable")
	f.m.rotationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.RotationNotificationFact, bool, error) {
		return fleet.RotationNotificationFact{}, false, sentinel
	}
	require.ErrorIs(t, f.m.rotationWarningJob(t.Context(), warningJob()), sentinel)
	f.m.rotationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.RotationNotificationFact, bool, error) {
		return fleet.RotationNotificationFact{State: "FAILED", RequestedBy: 11, RetireOldAt: f.m.now().Add(time.Hour)}, true, nil
	}
	f.err = sentinel
	require.ErrorIs(t, f.m.rotationWarningJob(t.Context(), warningJob()), sentinel)
	q := query.BindRotationAlerts(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	f.err = nil
	require.NoError(t, f.m.rotationWarningJob(t.Context(), warningJob()))
	var rows []model.RotationAlert
	require.NoError(t, f.db.Table(f.m.names.RotationAlerts.String()).Find(&rows).Error)
	require.Len(t, rows, 1)
	f.m.rotationFacts = nil
	require.ErrorIs(t, f.m.rotationWarningJob(t.Context(), warningJob()), ErrUnavailable)
	for _, j := range []jobs.Job{{}, {TenantID: 7, Payload: json.RawMessage(`{}`)}, {TenantID: 7, Payload: json.RawMessage(`invalid`)}} {
		require.Error(t, f.m.rotationWarningJob(t.Context(), j))
	}
}
