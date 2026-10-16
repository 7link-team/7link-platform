package dlock

import (
	"context"
	"errors"
	"strings"
	"testing"

	modulepkg "7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTransactionGuardProviderAndRollback(t *testing.T) {
	rt, db := newModuleRuntime(t)
	m := NewModule(rt)
	services := modulepkg.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	guard, err := modulepkg.Resolve[TransactionGuard](services)
	require.NoError(t, err)
	abort := errors.New("abort")
	for _, rollback := range []bool{true, false} {
		err = db.Transaction(func(tx *gorm.DB) error {
			require.NoError(t, guard(t.Context(), tx, "jobs:pending:1"))
			require.NoError(t, guard(t.Context(), tx, "jobs:pending:1"))
			require.ErrorIs(t, guard(t.Context(), tx, ""), ErrInvalidTransaction)
			require.ErrorIs(t, guard(t.Context(), tx, strings.Repeat("x", 180)), ErrInvalidTransaction)
			require.ErrorIs(t, guard(t.Context(), tx, string([]byte{0xff})), ErrInvalidTransaction)
			require.ErrorIs(t, NewModule(nil).transactionGuard(t.Context(), tx, "x"), ErrDatabaseUnavailable)
			if rollback {
				return abort
			}
			return nil
		})
		if rollback {
			require.ErrorIs(t, err, abort)
		} else {
			require.NoError(t, err)
		}
		var count int64
		require.NoError(t, db.Table("dlocks").Count(&count).Error)
		if rollback {
			require.Zero(t, count)
		} else {
			require.EqualValues(t, 1, count)
		}
	}
	require.ErrorIs(t, guard(t.Context(), nil, "x"), ErrInvalidTransaction)
	require.ErrorIs(t, guard(t.Context(), db, "x"), ErrInvalidTransaction)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return guard(ctx, tx, "x") }))
}
