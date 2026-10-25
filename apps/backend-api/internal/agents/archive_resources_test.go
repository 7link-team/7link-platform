package agents

import (
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestProjectArchiveReminderRequiresExplicitDisposition(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	now := time.Now().UTC()
	m.now = func() time.Time { return now }
	var reminder ReminderView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		reminder, err = m.reminderOn(ctx, tx, 1, 7, ReminderInput{ProjectID: new(guid.ID(3)), ThreadID: 4, Title: "Review", DueAt: now.Add(time.Hour), MaxFires: 1})
		return err
	}))
	read := func(tenant, project guid.ID) []delivery.ProjectArchiveResource {
		var rows []delivery.ProjectArchiveResource
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			rows, err = m.projectArchiveResourcesOn(ctx, tx, tenant, project)
			return err
		}))
		return rows
	}
	rows := read(1, 3)
	require.Len(t, rows, 1)
	require.False(t, rows[0].Ready)
	require.Empty(t, read(2, 3))
	require.Empty(t, read(1, 4))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.changeReminderOn(ctx, tx, 1, 7, reminder.ID, 1, "PAUSED")
		return err
	}))
	rows = read(1, 3)
	require.True(t, rows[0].Ready)
	require.Equal(t, "PAUSED", rows[0].State)
	require.Equal(t, 2, rows[0].Version)
	_, err := m.projectArchiveResourcesOn(ctx, db, 1, 3)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestReminderWritesFenceArchivedProjectAndPreserveHistory(t *testing.T) {
	for _, actor := range []guid.ID{2, 7} {
		t.Run(actor.String(), func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, actor)
			now := time.Now().UTC().Truncate(time.Millisecond)
			m.now = func() time.Time { return now }
			in := ReminderInput{ProjectID: new(guid.ID(3)), ThreadID: 4, Title: "Review", DueAt: now.Add(time.Hour), MaxFires: 1}
			var paused ReminderView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				created, err := m.reminderOn(ctx, tx, 1, 7, in)
				if err != nil {
					return err
				}
				paused, err = m.changeReminderOn(ctx, tx, 1, 7, created.ID, created.Version, "PAUSED")
				return err
			}))
			archived := true
			original := m.threads.CheckOn
			m.threads.CheckOn = func(anchorCtx context.Context, tx *gorm.DB, tenant, project, thread, agent guid.ID) (int64, error) {
				_, active := tx.Statement.ConnPool.(gorm.TxCommitter)
				require.True(t, active)
				require.Equal(t, guid.ID(3), project)
				if archived && core.EntityMutationFromContext(anchorCtx) {
					return 0, ErrForbidden
				}
				return original(anchorCtx, tx, tenant, project, thread, agent)
			}
			q := query.BindReminder(db, m.names)
			before, err := q.WithContext(ctx).Where(q.ID.Eq(paused.ID)).First()
			require.NoError(t, err)
			countBefore, err := q.WithContext(ctx).Count()
			require.NoError(t, err)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.reminderOn(ctx, tx, 1, 7, in)
				return err
			}), ErrForbidden)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.changeReminderOn(ctx, tx, 1, 7, paused.ID, paused.Version, "ACTIVE")
				return err
			}), ErrForbidden)
			after, err := q.WithContext(ctx).Where(q.ID.Eq(paused.ID)).First()
			require.NoError(t, err)
			require.Equal(t, before, after)
			countAfter, err := q.WithContext(ctx).Count()
			require.NoError(t, err)
			require.Equal(t, countBefore, countAfter)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				rows, _, err := m.remindersOn(ctx, tx, 1, 7, 0, 10)
				require.Len(t, rows, 1)
				return err
			}), "archived reminder history stays readable")
			archived = false
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.changeReminderOn(ctx, tx, 1, 7, paused.ID, paused.Version, "ACTIVE")
				return err
			}), "existing SELF and tenant manager semantics remain sufficient")
		})
	}
}
