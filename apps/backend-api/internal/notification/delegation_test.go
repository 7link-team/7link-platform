package notification

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDelegationSchedulingAndWarningFailuresCannotCreateNotice(t *testing.T) {
	for _, mode := range []string{"missing", "source", "project", "ends", "admission", "queue", "job_payload", "job_database", "job_candidates", "job_visibility", "job_notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			fact := governance.DelegationNotificationFact{ProjectID: 81, GrantorID: 11, State: "ACTIVE", EndsAt: f.m.now().Add(time.Hour)}
			f.m.delegationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (governance.DelegationNotificationFact, bool, error) {
				if mode == "source" {
					return fact, false, ErrUnavailable
				}
				return fact, true, nil
			}
			switch mode {
			case "missing":
				f.m.delegationFacts = nil
			case "project":
				fact.ProjectID = 82
			case "ends":
				fact.EndsAt = time.Time{}
			case "admission":
				f.m.admission = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "queue":
				f.m.queue = &fixtureQueue{err: ErrUnavailable}
			case "job_candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "job_visibility":
				f.err = ErrUnavailable
			case "job_notify":
				f.ids.err = ErrUnavailable
			}
			var err error
			switch mode {
			case "job_payload":
				err = f.m.delegationWarningJob(t.Context(), jobs.Job{TenantID: 7, Payload: []byte(`{}`)})
			case "job_database":
				f.m.runtime = nil
				fallthrough
			case "job_candidates", "job_visibility", "job_notify":
				err = f.m.delegationWarningJob(t.Context(), jobs.Job{TenantID: 7, Payload: []byte(`{"delegation_id":"91"}`)})
			default:
				err = f.db.Transaction(func(tx *gorm.DB) error {
					return f.m.scheduleDelegationWarningOn(t.Context(), tx, events.RecordedEvent{TenantID: 7, ProjectID: 81, SubjectID: 91})
				})
			}
			require.Error(t, err)
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestDelegationEndedProjectionUsesSourceGrantorAndCurrentVisibility(t *testing.T) {
	f := newFixture(t)
	fact := governance.DelegationNotificationFact{ProjectID: 81, GrantorID: 11, State: "EXPIRED", EndReason: "WINDOW_ENDED"}
	f.m.delegationFacts = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (governance.DelegationNotificationFact, bool, error) {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 91, id)
		return fact, true, nil
	}
	event := events.RecordedEvent{ID: 1, TenantID: 7, ProjectID: 81, SubjectID: 91, ActorID: 12, Type: "delegation.expired", Data: json.RawMessage(`{"end_reason":"WINDOW_ENDED","grantee_principal_id":"12"}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, event) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 11, rows[0].RecipientPrincipalID)
	require.Equal(t, "governance.delegation_ended", rows[0].Type)
	visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.True(t, visible)
	fact.GrantorID = 12
	visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, visible)
	fact.GrantorID = 11
	f.members.active = false
	visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, visible)
}

func TestDelegationEndedRejectsMissingMismatchedAndManualFacts(t *testing.T) {
	for _, reason := range []string{"", "MANUAL", "WINDOW_ENDED"} {
		t.Run(reason, func(t *testing.T) {
			f := newFixture(t)
			f.m.delegationFacts = nil
			data, err := json.Marshal(map[string]string{"end_reason": reason})
			require.NoError(t, err)
			err = f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.projectEventOn(t.Context(), tx, events.RecordedEvent{TenantID: 7, Type: "delegation.revoked", SubjectID: 91, Data: data})
			})
			if reason == "MANUAL" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			q := query.BindNotifications(f.db, f.m.names)
			n, err := q.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestDelegationGrantedExcludesGrantorAndQualityEscapeUsesOwnersAndLeads(t *testing.T) {
	f := newFixture(t)
	f.m.delegationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (governance.DelegationNotificationFact, bool, error) {
		return governance.DelegationNotificationFact{ProjectID: 81, GrantorID: 11, State: "ACTIVE", EndsAt: f.m.now().Add(96 * time.Hour)}, true, nil
	}
	for _, typ := range []string{"delegation.granted", "quality.escape_recorded"} {
		for range 2 {
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.projectEventOn(t.Context(), tx, events.RecordedEvent{TenantID: 7, ProjectID: 81, SubjectID: 91, Type: typ, Data: json.RawMessage(`{}`)})
			}))
		}
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq("governance.delegation_granted")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	rows, err = q.WithContext(t.Context()).Where(q.Type.Eq("governance.quality_escape")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	f.allow = false
	visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], rows[0].RecipientPrincipalID)
	require.NoError(t, err)
	require.False(t, visible)
}

func TestDelegationExpiryWarningRechecksTimeStateAndGrantor(t *testing.T) {
	f := newFixture(t)
	now := f.m.now()
	f.m.now = func() time.Time { return now }
	fact := governance.DelegationNotificationFact{ProjectID: 81, GrantorID: 11, State: "ACTIVE", EndsAt: now.Add(96 * time.Hour)}
	f.m.delegationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (governance.DelegationNotificationFact, bool, error) {
		return fact, true, nil
	}
	job := jobs.Job{TenantID: 7, Payload: json.RawMessage(`{"delegation_id":"91"}`)}
	require.Error(t, f.m.delegationWarningJob(t.Context(), job), "an early attempt must be rescheduled")
	now = now.Add(25 * time.Hour)
	for range 2 {
		require.NoError(t, f.m.delegationWarningJob(t.Context(), job))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq("governance.delegation_expiring")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	f.allow = false
	for _, row := range rows {
		visible, err := f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
		require.NoError(t, err)
		require.Equal(t, row.RecipientPrincipalID == 11, visible)
	}
	fact.State = "REVOKED"
	require.NoError(t, f.m.delegationWarningJob(t.Context(), job))
	visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], rows[0].RecipientPrincipalID)
	require.NoError(t, err)
	require.False(t, visible)
	require.Error(t, f.m.delegationWarningJob(t.Context(), jobs.Job{TenantID: 7, Payload: json.RawMessage(`{}`)}))
}
