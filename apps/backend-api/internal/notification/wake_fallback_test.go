package notification

import (
	"errors"
	"testing"

	"7link.com/platform/internal/agents"
	agentsi18n "7link.com/platform/internal/agents/i18n"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWakeFallbackPersistsOwnerNoticeAndRollsBackWithCaller(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, agentsi18n.Register())
	failure := agents.WakeFailure{TenantID: 7, AgentPrincipalID: 77, OwnerPrincipalID: 11, Failures: 20}
	err := f.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, f.m.wakeFallbackOn(t.Context(), tx, failure))
		return errors.New("caller rollback")
	})
	require.Error(t, err)
	q := query.BindNotifications(f.db, f.m.names)
	count, e := q.WithContext(t.Context()).Where(q.Type.Eq("agents.wake_failed")).Count()
	require.NoError(t, e)
	require.Zero(t, count)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.wakeFallbackOn(t.Context(), tx, failure) }))
	rows, e := q.WithContext(t.Context()).Where(q.Type.Eq("agents.wake_failed")).Find()
	require.NoError(t, e)
	require.Len(t, rows, 1)
	require.Equal(t, failure.OwnerPrincipalID, rows[0].RecipientPrincipalID)
	require.Contains(t, rows[0].Params, `"subject_id":"77"`)
	failure.Failures = 19
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.wakeFallbackOn(t.Context(), tx, failure) }), ErrInvalid)
}
func TestWakeConnectorOptionalInIsolatedNotificationHarness(t *testing.T) {
	f := newFixture(t)
	s := module.NewServices()
	require.NoError(t, s.Seal())
	require.NoError(t, f.m.injectWake(s))
}
