package yunxiao

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIntakeNotifyTargetsPersistAndRecheckActualHumanSourceVisibility(t *testing.T) {
	m, db, ctx := workHarness(t)
	in := intakeSettingsFixture()
	in.Enabled = true
	in.NotifyPrincipalIDs = []guid.ID{12, 13}
	var channel IntakeChannelView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		channel, err = m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 0, in)
		return err
	}))
	read := func() []guid.ID {
		var ids []guid.ID
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			ids, err = m.intakeNotifyTargetsOn(context.Background(), tx, 1, 99, channel.ID)
			return err
		}))
		return ids
	}
	require.Equal(t, in.NotifyPrincipalIDs, read())
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id != 12, nil
	}
	require.Equal(t, []guid.ID{13}, read(), "inactive target removed at delivery time")
	in.NotifyPrincipalIDs = []guid.ID{12}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 1, in); return err }), ErrForbidden)
	for _, ids := range [][]guid.ID{{0}, {12, 12}} {
		in.NotifyPrincipalIDs = ids
		require.ErrorIs(t, validateIntakeSettings(in), ErrInvalid)
	}
}
