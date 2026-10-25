package governance

import (
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestProjectTerminalRevokesRealDelegationWithFenceAndRollback(t *testing.T) {
	m, db, ctx, pub, in := delegationHarness(t)
	v, e := grantDelegation(t, m, db, ctx, in)
	require.NoError(t, e)
	call := func(ids []guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error {
			return m.endProjectAuthorityOn(ctx, tx, 1, 99, "ROLE:delivery_lead", "Explicit terminal decision", ids)
		})
	}
	require.ErrorIs(t, call(nil), ErrVersion)
	pub.Fail = true
	require.Error(t, call([]guid.ID{3}))
	pub.Fail = false
	q := query.BindDelegations(db, m.seed.names)
	before, e := q.WithContext(ctx).Where(q.ID.Eq(v.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "ACTIVE", before.State)
	require.NoError(t, call([]guid.ID{3}))
	after, e := q.WithContext(ctx).Where(q.ID.Eq(v.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "REVOKED", after.State)
	require.Equal(t, "PROJECT_TERMINAL", *after.EndReason)
	require.EqualValues(t, 0, seedCount(t, db, "binding_probe"))
	require.ErrorIs(t, m.endProjectAuthorityOn(ctx, db, 1, 99, "ROLE:delivery_lead", "reason", []guid.ID{3}), ErrInvalidArgument)
}
