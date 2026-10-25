package delivery

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIterationIdentityUsesCurrentCanonicalWorkAndAuthority(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		identity, err := m.iterationWorkOn(ctx, tx, 1, 99, work.ID)
		require.NoError(t, err)
		require.Equal(t, work.State, identity.State)
		require.Equal(t, work.Type, identity.Type)
		require.Equal(t, work.AccountablePrincipalID, identity.PrincipalID)
		_, err = m.iterationWorkOn(ctx, tx, 1, 100, work.ID)
		require.Error(t, err, "another project cannot reuse a valid work identifier")
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err = m.iterationWorkOn(canceled, tx, 1, 99, work.ID)
		require.ErrorIs(t, err, context.Canceled)
		return nil
	}))
}
