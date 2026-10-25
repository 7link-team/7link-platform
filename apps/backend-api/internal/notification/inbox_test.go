package notification

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/i18n"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestInboxMutationStorageAndLockFailuresPreserveUnread(t *testing.T) {
	for _, mode := range []string{"lock", "read", "update", "mark_lock", "mark_update"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			id := f.notify(t, sample())[0]
			if mode == "lock" || mode == "mark_lock" {
				f.m.principal.LockActive = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			} else if mode == "read" {
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("inbox_read", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Notifications.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			} else {
				require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("inbox_update", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Notifications.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			yes := true
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				if mode == "mark_lock" || mode == "mark_update" {
					_, err := f.m.markReadOn(t.Context(), tx, 11, 7, id)
					return err
				}
				_, err := f.m.patchOn(t.Context(), tx, 11, id, &yes, nil)
				return err
			}))
			if mode == "read" {
				require.NoError(t, f.db.Callback().Query().Remove("inbox_read"))
			}
			if mode == "update" || mode == "mark_update" {
				require.NoError(t, f.db.Callback().Update().Remove("inbox_update"))
			}
			row, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).First()
			require.NoError(t, err)
			require.Nil(t, row.ReadAt)
			require.Nil(t, row.ArchivedAt)
		})
	}
}

func TestHistoricalMachineNoticeRendersWithoutMutatingStoredParams(t *testing.T) {
	params := map[string]string{"subject_id": "81"}
	normalized := notificationTemplateParams("notification.machine_offline", params)
	require.Equal(t, "81", normalized["machine_id"])
	require.Equal(t, "1", normalized["source_count"])
	require.NotContains(t, params, "machine_id")
	normalized = notificationTemplateParams("notification.machine_offline", map[string]string{"subject_id": "81", "source_count": "3"})
	require.Equal(t, "3", normalized["source_count"])
	f := newFixture(t)
	for i := 0; i < 3; i++ {
		n := sample()
		n.DedupeKey = "page:" + guid.ID(i+1).String()
		f.notify(t, n)
	}
	rows, err := f.m.inboxRows(t.Context(), f.db, 11, 7, 0, "", 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	next, err := f.m.inboxRows(t.Context(), f.db, 11, 7, rows[0].ID, "", 1)
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Less(t, next[0].ID, rows[0].ID)
	_, err = f.m.inboxRows(t.Context(), f.db, 0, 7, 0, "", 1)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestInboxVisibilitySummaryReadArchiveAndOwnership(t *testing.T) {
	f := newFixture(t)
	ids := f.notify(t, sample())
	id := ids[0]
	yes := true
	no := false
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		rows, err := f.m.inboxRows(t.Context(), tx, 11, 7, 0, "unread", 20)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		summary, err := f.m.summaryOn(t.Context(), tx, 11, 0)
		require.NoError(t, err)
		require.Equal(t, 1, summary.Unread)
		require.Equal(t, 1, summary.ActionRequired)
		require.Len(t, summary.ByTenant, 1)
		_, err = f.m.patchOn(t.Context(), tx, 12, id, &yes, nil)
		require.ErrorIs(t, err, ErrNotFound)
		_, err = f.m.patchOn(t.Context(), tx, 11, 999, &yes, nil)
		require.ErrorIs(t, err, ErrNotFound)
		_, err = f.m.patchOn(t.Context(), tx, 0, id, &yes, nil)
		require.ErrorIs(t, err, ErrInvalid)
		_, err = f.m.patchOn(t.Context(), tx, 11, id, nil, nil)
		require.ErrorIs(t, err, ErrInvalid)
		_, err = f.m.patchOn(t.Context(), tx, 11, id, &yes, nil)
		require.NoError(t, err)
		rows, err = f.m.inboxRows(t.Context(), tx, 11, 7, 0, "read", 20)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		_, err = f.m.patchOn(t.Context(), tx, 11, id, &no, &yes)
		require.NoError(t, err)
		rows, err = f.m.inboxRows(t.Context(), tx, 11, 7, 0, "", 20)
		require.NoError(t, err)
		require.Empty(t, rows)
		_, err = f.m.patchOn(t.Context(), tx, 11, id, nil, &no)
		return err
	}))
	f.allow = false
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		summary, err := f.m.summaryOn(t.Context(), tx, 11, 7)
		require.NoError(t, err)
		require.Zero(t, summary.Unread)
		_, err = f.m.patchOn(t.Context(), tx, 11, id, &yes, nil)
		require.ErrorIs(t, err, ErrNotFound)
		return nil
	}))
	f.allow = true
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		marked, err := f.m.markReadOn(t.Context(), tx, 11, 7, id)
		require.NoError(t, err)
		require.Equal(t, 1, marked)
		marked, err = f.m.markReadOn(t.Context(), tx, 11, 7, id)
		require.NoError(t, err)
		require.Zero(t, marked)
		_, err = f.m.markReadOn(t.Context(), tx, 11, -1, id)
		require.ErrorIs(t, err, ErrInvalid)
		return nil
	}))
	n := sample()
	n.DedupeKey = "urgent"
	n.Severity = Urgent
	f.notify(t, n)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		s, err := f.m.summaryOn(t.Context(), tx, 11, 7)
		require.NoError(t, err)
		require.Equal(t, 1, s.Urgent)
		rows, err := f.m.inboxRows(t.Context(), tx, 12, 0, 0, "", 10)
		require.NoError(t, err)
		require.Empty(t, rows)
		return nil
	}))
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		v, err := view(i18n.WithLocale(t.Context(), locale), &model.Notification{ID: guid.ID(3), Params: `{"subject_id":"91","type":"machine.offline"}`})
		require.NoError(t, err)
		require.NotEmpty(t, v.Title)
		require.Contains(t, v.Body, "91")
	}
	_, err := view(t.Context(), &model.Notification{Params: "broken"})
	require.Error(t, err)
	_, err = view(t.Context(), &model.Notification{Params: `{}`})
	require.Error(t, err)
}
