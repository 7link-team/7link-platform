package notification

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnrollmentNoticesUseActualApproverAndSuppressEverOnlineWarning(t *testing.T) {
	f := newFixture(t)
	start := f.m.now()
	now := start
	f.m.now = func() time.Time { return now }
	fact := fleet.EnrollmentNotificationFact{MachineID: 81, ApproverID: 11, ApprovedAt: start}
	f.m.enrollmentFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.EnrollmentNotificationFact, bool, error) {
		return fact, true, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, SubjectID: 91, SubjectKind: "machine_enrollment", Type: "machine.enrollment_changed", Data: json.RawMessage(`{"state":"APPROVED"}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq("machine.enrolled")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID, "confirmation excludes the actual approver")
	require.Equal(t, 1, rows[0].MergeCount, "replay cannot count the enrollment twice")
	raw, err := json.Marshal(enrollmentWarning{ID: 91})
	require.NoError(t, err)
	job := jobs.Job{TenantID: 7, Payload: raw}
	require.NoError(t, f.m.enrollmentWarningJob(t.Context(), job))
	n, err := q.WithContext(t.Context()).Where(q.Type.Eq("machine.never_online")).Count()
	require.NoError(t, err)
	require.Zero(t, n)
	now = start.Add(10 * time.Minute)
	for range 2 {
		require.NoError(t, f.m.enrollmentWarningJob(t.Context(), job))
	}
	rows, err = q.WithContext(t.Context()).Where(q.Type.Eq("machine.never_online")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2, "current managers plus the approver")
	require.Equal(t, "/admin/7/machines/81", rows[0].LinkPath)
	fact.EverOnline = true
	for _, row := range rows {
		visible, err := f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
		require.NoError(t, err)
		require.False(t, visible, "even a subsequent offline machine was once online")
	}
}

func TestEnrollmentProjectionAndWarningFailuresLeaveNoNotice(t *testing.T) {
	for _, mode := range []string{"payload", "missing", "source", "admission", "queue", "candidates", "order", "visibility", "notify", "job_payload", "job_database"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.enrollmentFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.EnrollmentNotificationFact, bool, error) {
				if mode == "source" {
					return fleet.EnrollmentNotificationFact{}, false, ErrUnavailable
				}
				return fleet.EnrollmentNotificationFact{MachineID: 81, ApproverID: 11, ApprovedAt: f.m.now().Add(-time.Hour)}, true, nil
			}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, SubjectID: 91, Data: []byte(`{"state":"APPROVED"}`)}
			switch mode {
			case "payload":
				e.Data = []byte("[")
			case "missing":
				f.m.enrollmentFacts = nil
			case "admission":
				f.m.admission = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "queue":
				f.m.queue = &fixtureQueue{err: ErrUnavailable}
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			var err error
			if mode == "job_payload" {
				err = f.m.enrollmentWarningJob(t.Context(), jobs.Job{TenantID: 7, Payload: []byte(`{}`)})
			} else if mode == "job_database" {
				f.m.runtime = nil
				err = f.m.enrollmentWarningJob(t.Context(), jobs.Job{TenantID: 7, Payload: []byte(`{"id":"91"}`)})
			} else {
				err = f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEnrollmentOn(t.Context(), tx, e) })
			}
			require.Error(t, err)
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
			var count int64
			require.NoError(t, f.db.Table("test_notification_jobs").Count(&count).Error)
			require.Zero(t, count, "notification failure rolls back scheduled warning")
		})
	}
}

func TestEnrollmentAudienceMalformedAndMissingSourceFailClosed(t *testing.T) {
	f := newFixture(t)
	row := &model.Notification{TenantID: 7, Type: "machine.enrolled", Params: `{"subject_id":"91"}`}
	yes, err := f.m.enrollmentAudienceOn(t.Context(), f.db, row, 11)
	require.False(t, yes)
	require.ErrorIs(t, err, ErrUnavailable)
	f.m.enrollmentFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.EnrollmentNotificationFact, bool, error) {
		return fleet.EnrollmentNotificationFact{}, false, ErrUnavailable
	}
	for _, raw := range []string{"[]", `{"subject_id":"bad"}`, `{"subject_id":"91"}`} {
		row.Params = raw
		yes, err = f.m.enrollmentAudienceOn(t.Context(), f.db, row, 11)
		require.False(t, yes)
		require.Error(t, err)
	}
}
