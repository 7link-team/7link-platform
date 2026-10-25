package notification

import (
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestPlatformDeadlettersUseSystemAuthorityAndCurrentRevocation(t *testing.T) {
	f := newFixture(t)
	f.members.active = false
	f.m.platformPermission = func(_ context.Context, _ *gorm.DB, code string, id guid.ID) (bool, error) {
		require.Equal(t, "system_job_manage", code)
		return id == 11, nil
	}
	e := events.RecordedEvent{ID: 70, TenantID: 7, SubjectID: 31, Type: "job.dead_lettered", Data: json.RawMessage(`{"kind":"files.export","attempts":8}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	r := rows[0]
	require.Zero(t, r.TenantID)
	require.Equal(t, guid.ID(11), r.RecipientPrincipalID)
	require.Equal(t, 2, r.MergeCount)
	require.Equal(t, "jobs.dead:files.export", *r.DedupeKey)
	require.Equal(t, "/system/jobs?state=DEAD", r.LinkPath)
	v, err := view(t.Context(), r)
	require.NoError(t, err)
	require.Contains(t, v.Body, "files.export")
	require.Contains(t, v.Body, "8")
	visible, err := f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.True(t, visible)
	f.m.platformPermission = func(context.Context, *gorm.DB, string, guid.ID) (bool, error) { return false, nil }
	visible, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.False(t, visible)
}
func TestPlatformEmailPreferencesAndParkedAlerts(t *testing.T) {
	f := newFixture(t)
	c, messages := smtpFixture(t, 250)
	enableEmail(t, f, c)
	f.m.principal.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{Email: "reader@example.test"}, true, nil
	}
	f.m.candidates = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]guid.ID, error) {
		if after == 0 {
			return []guid.ID{11}, nil
		}
		return nil, nil
	}
	f.members.active = false
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		p, err := f.m.preferenceOn(t.Context(), tx, 11, 0, "*")
		if err != nil {
			return err
		}
		p.Email = "IMMEDIATE"
		_, err = f.m.savePreferenceOn(t.Context(), tx, 11, 0, "*", p, 0)
		return err
	}))
	e := events.RecordedEvent{SubjectID: 31, Type: "job.dead_lettered", Data: json.RawMessage(`{"kind":"files.export","attempts":8}`)}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	d := query.BindDeliveries(f.db, f.m.names)
	rows, err := d.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NoError(t, f.m.deliverJob(t.Context(), deliveryJob(t, rows[0])))
	require.Len(t, messages(), 1)
	require.Contains(t, messages()[0], "files.export")
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.parkedNotificationOn(t.Context(), tx, events.ParkedNotice{Subscriber: "delivery.projector", Count: 3, EventID: 55, TenantID: 7})
	}))
	n := query.BindNotifications(f.db, f.m.names)
	r, err := n.WithContext(t.Context()).Where(n.Type.Eq("events.parked")).First()
	require.NoError(t, err)
	v, err := view(t.Context(), r)
	require.NoError(t, err)
	require.Contains(t, v.Body, "delivery.projector")
	require.Contains(t, v.Body, "3")
	require.Equal(t, "events.parked:delivery.projector", *r.DedupeKey)
	f.m.platformPermission = nil
	_, err = f.m.preferenceOn(t.Context(), f.db, 11, 0, "*")
	require.ErrorIs(t, err, ErrUnavailable)
}
func TestMentionsResolveThroughOwnerAndRecheckOnRead(t *testing.T) {
	f := newFixture(t)
	allowed := true
	f.m.mentions = collab.MentionNotifications{RecipientsOn: func(_ context.Context, _ *gorm.DB, tenant, message guid.ID) ([]guid.ID, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(81), message)
		return []guid.ID{11}, nil
	}, VisibleOn: func(_ context.Context, _ *gorm.DB, tenant, message, principal guid.ID) (bool, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(81), message)
		require.Equal(t, guid.ID(11), principal)
		return allowed, nil
	}}
	e := events.RecordedEvent{TenantID: 7, ProjectID: 3, SubjectID: 81, ActorID: 12, Type: "collab.message_posted", Data: json.RawMessage(`{"channel_id":"61","mentions":["12"]}`)}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	q := query.BindNotifications(f.db, f.m.names)
	r, err := q.WithContext(t.Context()).First()
	require.NoError(t, err)
	require.Equal(t, guid.ID(11), r.RecipientPrincipalID)
	require.Contains(t, r.LinkPath, "/entities/3/conversations?channel_id=61")
	visible, err := f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.True(t, visible)
	allowed = false
	visible, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.False(t, visible)
	f.m.mentions.RecipientsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	sentinel := errors.New("collab unavailable")
	f.m.mentions.RecipientsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, sentinel }
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }), sentinel)
}
