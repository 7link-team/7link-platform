package notification

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestVisibilityFailsClosedForInvalidOwnersAndCurrentProjectAuthority(t *testing.T) {
	f := newFixture(t)
	r := &model.Notification{TenantID: 7, RecipientPrincipalID: 11, Params: `{}`}
	for _, principal := range []guid.ID{0, 12} {
		visible, err := f.m.visibleOn(t.Context(), f.db, r, principal)
		require.NoError(t, err)
		require.False(t, visible)
	}
	r.Type = "fleet.agent_error_persisting"
	f.m.activity.SnapshotOn = nil
	_, err := f.m.visibleOn(t.Context(), f.db, r, 11)
	require.ErrorIs(t, err, ErrUnavailable)
	f.m.activity.SnapshotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (agents.ActivitySnapshot, bool, error) {
		return agents.ActivitySnapshot{}, false, nil
	}
	for _, params := range []string{`bad`, `{}`, `{"subject_id":"0"}`} {
		r.Params = params
		_, err = f.m.visibleOn(t.Context(), f.db, r, 11)
		require.Error(t, err)
	}
	r.Type = "channel.mentioned"
	f.m.mentions.VisibleOn = nil
	_, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.ErrorIs(t, err, ErrUnavailable)
	r.Type = "project.stage_changed"
	r.ProjectID = 81
	r.Permission = "project_read"
	for _, allow := range []bool{true, false} {
		f.allow = allow
		visible, err := f.m.visibleOn(t.Context(), f.db, r, 11)
		require.NoError(t, err)
		require.Equal(t, allow, visible)
	}
	f.m.projectAuthority = nil
	f.allow = true
	_, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestNotifyDedupeWindowReadRolloverRollbackAndIsolation(t *testing.T) {
	f := newFixture(t)
	n := sample()
	n.Recipients = []guid.ID{12, 11, 11}
	first := f.notify(t, n)
	require.Len(t, first, 2)
	n.Params["subject_id"] = "92"
	require.Equal(t, first, f.notify(t, n))
	var rows []model.Notification
	require.NoError(t, f.db.Table(f.m.names.Notifications.String()).Order("id").Find(&rows).Error)
	require.Equal(t, 2, rows[0].MergeCount)
	require.Contains(t, rows[0].Params, "92")
	yes := true
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.patchOn(t.Context(), tx, 11, first[0], &yes, nil); return err }))
	next := f.notify(t, n)
	require.NotEqual(t, first[0], next[0])
	require.Equal(t, first[1], next[1])
	f.m.now = func() time.Time { return time.Date(2026, 10, 20, 12, 31, 0, 0, time.UTC) }
	later := f.notify(t, n)
	require.NotEqual(t, next, later)
	abort := errors.New("abort")
	n.DedupeKey = "rollback"
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.Notify(t.Context(), tx, n)
		require.NoError(t, err)
		return abort
	}), abort)
	var count int64
	require.NoError(t, f.db.Table(f.m.names.Notifications.String()).Where("dedupe_key = ?", "rollback").Count(&count).Error)
	require.Zero(t, count)
	f.members.active = false
	require.Empty(t, f.notify(t, n))
	f.members.active = true
	f.human = false
	require.Empty(t, f.notify(t, n))
	f.human = true
}
func TestNotifyValidationAndDependencyFailures(t *testing.T) {
	f := newFixture(t)
	_, err := f.m.Notify(t.Context(), f.db, sample())
	require.ErrorIs(t, err, ErrInvalid)
	_, err = f.m.Notify(t.Context(), nil, sample())
	require.ErrorIs(t, err, ErrInvalid)
	for _, change := range []func(*Notification){func(n *Notification) { n.TenantID = 0 }, func(n *Notification) { n.ProjectID = -1 }, func(n *Notification) { n.Type = "bad" }, func(n *Notification) { n.Recipients = nil }, func(n *Notification) { n.Recipients = []guid.ID{0} }, func(n *Notification) { n.Recipients = make([]guid.ID, 201) }, func(n *Notification) { n.DedupeKey = strings.Repeat("x", 161) }, func(n *Notification) { n.TemplateKey = strings.Repeat("x", 161) }, func(n *Notification) { n.LinkPath = "//evil.invalid" }, func(n *Notification) { n.Severity = "INVALID" }, func(n *Notification) { n.Params = map[string]string{"large": strings.Repeat("x", 8193)} }} {
		n := sample()
		change(&n)
		require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.Notify(t.Context(), tx, n); return e }), ErrInvalid)
	}
	for _, link := range []string{"https://evil.invalid", "/\\evil", "/x\n", "/%zz"} {
		require.False(t, validLink(link))
	}
	require.True(t, validLink("/user/notifications"))
	require.True(t, validLink(""))
	f.ids.err = errors.New("ids")
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.Notify(t.Context(), tx, sample()); return e }))
	f.ids.err = nil
	f.ids.zero = true
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.Notify(t.Context(), tx, sample()); return e }), ErrUnavailable)
	f.ids.zero = false
	f.err = errors.New("principal")
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.Notify(t.Context(), tx, sample()); return e }))
	f.err = nil
	f.members.err = errors.New("membership")
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.Notify(t.Context(), tx, sample()); return e }))
	f.members.err = nil
	f.m.ids = nil
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.Notify(t.Context(), tx, sample()); return e }), ErrUnavailable)
	_ = context.Background()
}

func TestNotifyEmailAdmissionAndMembershipFailuresAreAtomic(t *testing.T) {
	for _, mode := range []string{"missing_admission", "admission", "rate_guard", "membership"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			c, _ := smtpFixture(t)
			enableEmail(t, f, c)
			sentinel := errors.New("quota owner unavailable")
			switch mode {
			case "missing_admission":
				f.m.admission = nil
			case "admission":
				f.m.admission = func(context.Context, *gorm.DB, guid.ID) error { return sentinel }
			case "rate_guard":
				f.m.guard = func(context.Context, *gorm.DB, string) error { return sentinel }
			case "membership":
				f.members.err = sentinel
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.Notify(t.Context(), tx, sample()); return err }))
			for _, table := range []string{f.m.names.Notifications.String(), f.m.names.Deliveries.String(), "test_notification_jobs", "test_notification_events"} {
				var count int64
				require.NoError(t, f.db.Table(table).Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}
}

func TestNotifyStorageFailuresPreserveExistingUnreadGroup(t *testing.T) {
	for _, mode := range []string{"read", "merge", "release", "insert", "id_error", "id_zero", "human"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			n := sample()
			id := f.notify(t, n)[0]
			if mode == "release" {
				now := f.m.now().Add(2 * time.Minute)
				f.m.now = func() time.Time { return now }
			}
			if mode == "insert" || mode == "id_error" || mode == "id_zero" {
				n.DedupeKey = "another-occurrence"
			}
			switch mode {
			case "read":
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("notification_read_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Notifications.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "merge", "release":
				require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("notification_update_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Notifications.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "insert":
				require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("notification_insert_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Notifications.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "id_error":
				f.ids.err = ErrUnavailable
			case "id_zero":
				f.ids.zero = true
			case "human":
				f.m.principal.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			}
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.Notify(t.Context(), tx, n); return e }), ErrUnavailable)
			require.NoError(t, f.db.Callback().Query().Remove("notification_read_failure"))
			require.NoError(t, f.db.Callback().Update().Remove("notification_update_failure"))
			require.NoError(t, f.db.Callback().Create().Remove("notification_insert_failure"))
			q := query.BindNotifications(f.db, f.m.names)
			rows, e := q.WithContext(t.Context()).Find()
			require.NoError(t, e)
			require.Len(t, rows, 1)
			require.Equal(t, id, rows[0].ID)
			require.Equal(t, 1, rows[0].MergeCount)
			require.Equal(t, n.DedupeKey == "machine:91", *rows[0].DedupeSlot == n.DedupeKey)
		})
	}
}
