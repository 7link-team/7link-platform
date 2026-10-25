package notification

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestErasePrincipalNotificationData(t *testing.T) {
	f := newFixture(t)
	ids := f.notify(t, sample())
	s := query.BindSourceReceipts(f.db, f.m.names)
	require.NoError(t, s.WithContext(t.Context()).Create(&model.SourceReceipt{TenantID: 7, Type: "interaction.requested", OccurrenceID: 91, SourceID: 91, RecipientPrincipalID: 11, NotificationID: ids[0], CreatedAt: f.m.now()}))
	i := query.BindIntakeReceipts(f.db, f.m.names)
	require.NoError(t, i.WithContext(t.Context()).Create(&model.IntakeReceipt{TenantID: 7, IntakeID: 91, ChannelID: 90, RecipientPrincipalID: 11, ProjectID: 81, NotificationID: ids[0], CreatedAt: f.m.now()}))
	other := sample()
	other.Recipients = []guid.ID{12}
	f.notify(t, other)
	require.ErrorIs(t, ErasePrincipalData(t.Context(), f.db, "n_", 11, f.m.now()), ErrInvalid)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return ErasePrincipalData(t.Context(), tx, "n_", 11, f.m.now()) }))
	q := query.BindNotifications(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Where(q.RecipientPrincipalID.Eq(guid.ID(11))).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	count, err = s.WithContext(t.Context()).Where(s.RecipientPrincipalID.Eq(guid.ID(11))).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	count, err = i.WithContext(t.Context()).Where(i.RecipientPrincipalID.Eq(guid.ID(11))).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	count, err = q.WithContext(t.Context()).Where(q.RecipientPrincipalID.Eq(guid.ID(12))).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	d := query.BindDeliveries(f.db, f.m.names)
	count, err = d.WithContext(t.Context()).Where(d.RecipientPrincipalID.Eq(guid.ID(11))).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}
