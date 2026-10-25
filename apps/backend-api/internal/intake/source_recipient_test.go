package intake

import (
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestSourceRecipientRequiresActualCurrentContactInsteadOfAnyProjectClient(t *testing.T) {
	m, db, ctx, allowed, _ := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Update("principal_id", 4).Error)
	read := func(tenant, project, recipient guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.sourceRecipientOn(ctx, tx, tenant, project, 10, recipient) })
	}
	require.NoError(t, read(7, 20, 4))
	require.ErrorIs(t, read(7, 20, 5), gorm.ErrRecordNotFound)
	require.ErrorIs(t, read(7, 21, 4), gorm.ErrRecordNotFound)
	require.ErrorIs(t, read(8, 20, 4), ErrForbidden)
	require.ErrorIs(t, read(7, 20, 0), ErrInvalidArgument)
	*allowed = false
	require.ErrorIs(t, read(7, 20, 4), ErrForbidden)
	*allowed = true
	require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Update("archived_at", db.NowFunc()).Error)
	require.ErrorIs(t, read(7, 20, 4), gorm.ErrRecordNotFound)
}
