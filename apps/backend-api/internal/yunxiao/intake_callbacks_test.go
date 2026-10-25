package yunxiao

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/webhook"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type callbackSubscriptionCheck func(context.Context, *gorm.DB, guid.ID, guid.ID) error

func (f callbackSubscriptionCheck) ValidateOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
	return f(ctx, tx, tenant, id)
}

func TestIntakeCallbackPolicyBindsOptionalProviderAndRejectsInvalidReferences(t *testing.T) {
	m, db, ctx := workHarness(t)
	in := intakeSettingsFixture()
	id := guid.ID(9007199254740993)
	in.CallbackSubscriptionID = &id
	call := func(kind string, version int) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.saveIntakeChannelOn(ctx, tx, 1, 99, kind, version, in)
			return err
		})
	}
	require.ErrorIs(t, call("API", 0), ErrUnavailable)
	s := module.NewServices()
	require.NoError(t, m.provideIntakeCallbacks(s))
	require.NoError(t, s.Seal())
	r, err := module.Resolve[webhook.IntakeCallbackReceiver](s)
	require.NoError(t, err)
	fail := false
	checks := 0
	require.NoError(t, r.Bind(callbackSubscriptionCheck(func(_ context.Context, tx *gorm.DB, tenant, subscription guid.ID) error {
		checks++
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, id, subscription)
		require.NotNil(t, tx)
		if fail {
			return webhook.ErrSubscriptionNotFound
		}
		return nil
	})))
	require.NoError(t, call("API", 0))
	require.Equal(t, 1, checks)
	fail = true
	require.ErrorIs(t, call("API", 1), ErrInvalid)
	require.ErrorIs(t, call("EMAIL", 0), ErrInvalid)
	var version int
	require.NoError(t, db.Table(m.names.IntakeChannels.String()).Select("version").Where("kind='API'").Scan(&version).Error)
	require.Equal(t, 1, version, "rejected callback replacement must leave saved policy unchanged")
	in.CallbackSubscriptionID = nil
	require.NoError(t, call("API", 1), "clearing callback remains possible when outbound module is disabled")
}
