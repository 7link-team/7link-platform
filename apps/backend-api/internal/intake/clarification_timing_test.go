package intake

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestClarificationTimingUsesActualChannelAndPreservesUnavailablePolicy(t *testing.T) {
	m, db, ctx, allowed, _ := changeSourceHarness(t)
	read := func(id guid.ID) (ClarificationTiming, bool, error) {
		var out ClarificationTiming
		var found bool
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, found, err = m.clarificationTimingOn(ctx, tx, 7, 20, id)
			return err
		})
		return out, found, err
	}
	out, found, err := read(10)
	require.NoError(t, err)
	require.False(t, found)
	require.Zero(t, out)
	want := ClarificationTiming{ChannelID: 3, ChannelVersion: 4, ReminderHours: 12, TimeoutHours: 72, MaxReminders: 2}
	m.channelTiming = func(_ context.Context, tx *gorm.DB, tenant, project, channel guid.ID, kind string) (ClarificationTiming, bool, error) {
		require.NotNil(t, tx)
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 20, project)
		require.EqualValues(t, 3, channel)
		require.Equal(t, "SHARED_CHANNEL", kind)
		return want, true, nil
	}
	out, found, err = read(10)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, want, out)
	_, _, err = read(999)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	*allowed = false
	_, found, err = read(10)
	require.ErrorIs(t, err, ErrForbidden)
	require.False(t, found)
}

func TestClarificationExpiryStorageFailuresPreserveOpenRound(t *testing.T) {
	for _, stage := range []string{"missing", "wrong_round", "update", "cas"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, _, _ := changeSourceHarness(t)
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"status": "CLARIFYING", "open_round_id": 900}).Error)
			failure := errors.New("expiry source update failed")
			id, round := guid.ID(10), guid.ID(900)
			want := ErrConflict
			switch stage {
			case "missing":
				id = 999
				want = gorm.ErrRecordNotFound
			case "wrong_round":
				round = 901
			case "update":
				want = failure
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("regression_expiry", func(tx *gorm.DB) { tx.AddError(failure) }))
			case "cas":
				require.NoError(t, db.Callback().Update().After("gorm:update").Register("regression_expiry", func(tx *gorm.DB) { tx.RowsAffected = 0 }))
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.expireClarificationOn(ctx, tx, 7, 20, id, round, time.Now())
				return err
			})
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Update().Remove("regression_expiry"))
			var row struct {
				Status             string
				Version            int
				OpenRoundID        *guid.ID
				ClientUnresponsive bool
			}
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&row).Error)
			require.Equal(t, "CLARIFYING", row.Status)
			require.Equal(t, 1, row.Version)
			require.EqualValues(t, 900, *row.OpenRoundID)
			require.False(t, row.ClientUnresponsive)
		})
	}
}

func TestClarificationTimingConnectorAndExpiryRejectInvalidTransactions(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideChannelTiming(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[ChannelTimingConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, c.Bind(nil), ErrInvalidArgument)
	reader := ChannelTimingReader(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (ClarificationTiming, bool, error) {
		return ClarificationTiming{}, false, nil
	})
	require.NoError(t, c.Bind(reader))
	require.ErrorIs(t, c.Bind(reader), ErrInvalidArgument)
	_, err = m.expireClarificationOn(t.Context(), nil, 1, 2, 3, 4, time.Now())
	require.ErrorIs(t, err, ErrInvalidArgument)
}
