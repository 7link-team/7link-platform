package federation

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReplayRegistryAllowsOneConcurrentWinner(t *testing.T) {
	now := time.Now().UTC()
	env := newFederationEnvironment(t, "sl_")
	registries := []*replayRegistry{newReplayRegistry(NewRepository(env.runtime)), newReplayRegistry(NewRepository(env.runtime))}
	var winners atomic.Int32
	var group sync.WaitGroup
	for i := range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			claimed, err := registries[i%2].claim(t.Context(), "same-protocol-id", now.Add(time.Minute), now)
			assert.NoError(t, err)
			if claimed {
				winners.Add(1)
			}
		}()
	}
	group.Wait()
	assert.EqualValues(t, 1, winners.Load())
	// A new registry has no in-memory history, as after a process restart.
	claimed, err := newReplayRegistry(NewRepository(env.runtime)).claim(t.Context(), "same-protocol-id", now.Add(time.Minute), now)
	require.NoError(t, err)
	assert.False(t, claimed)
}

func TestReplayRegistryFailsClosedAndReclaimsExpiredEntries(t *testing.T) {
	now := time.Now().UTC()
	env := newFederationEnvironment(t)
	registry := env.service.replays
	for _, unavailable := range []*replayRegistry{nil, newReplayRegistry(nil), newReplayRegistry(&Repository{})} {
		claimed, err := unavailable.claim(t.Context(), "id", now.Add(time.Minute), now)
		assert.False(t, claimed)
		assert.ErrorIs(t, err, errReplayUnavailable)
	}
	for _, key := range []string{"", "expired"} {
		claimed, err := registry.claim(t.Context(), key, now, now)
		assert.False(t, claimed)
		assert.NoError(t, err)
	}
	claimed, err := registry.claim(t.Context(), "first", now.Add(time.Minute), now)
	require.NoError(t, err)
	require.True(t, claimed)
	// Advance persisted expiry, not a replica's clock; only the database clock
	// is allowed to decide what housekeeping can remove.
	require.NoError(t, env.db.Exec("UPDATE iam_federation_replay_claims SET expires_at = ?", now.Add(-time.Minute)).Error)
	claimed, err = registry.claim(t.Context(), "second", now.Add(3*time.Minute), now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, claimed)
	var count int64
	require.NoError(t, env.db.Table("iam_federation_replay_claims").Count(&count).Error)
	assert.EqualValues(t, 1, count)
	// Sweeping never makes a protocol message with an expired window valid.
	claimed, err = registry.claim(t.Context(), "first", now.Add(time.Minute), now.Add(time.Minute))
	require.NoError(t, err)
	assert.False(t, claimed)
	claimed, err = registry.claim(t.Context(), "second", now.Add(3*time.Minute), now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.False(t, claimed)
}

func TestReplayRegistryIdentifiersAreExactAndBounded(t *testing.T) {
	env := newFederationEnvironment(t, "sl_")
	now := time.Now().UTC()
	for _, key := range []string{"assertion:A", "assertion:a", "assertion:a ", strings.Repeat("a", 4096)} {
		claimed, err := env.service.replays.claim(t.Context(), key, now.Add(time.Minute), now)
		require.NoError(t, err)
		require.True(t, claimed, key)
		claimed, err = newReplayRegistry(NewRepository(env.runtime)).claim(t.Context(), key, now.Add(time.Minute), now)
		require.NoError(t, err)
		assert.False(t, claimed, key)
	}
	var keys []string
	require.NoError(t, env.db.Table("sl_iam_federation_replay_claims").Pluck("claim_key", &keys).Error)
	require.Len(t, keys, 4)
	for _, key := range keys {
		assert.Len(t, key, 64)
	}
}

func TestReplayRegistryUsesSharedClockForExpiry(t *testing.T) {
	env := newFederationEnvironment(t)
	now := time.Now().UTC()
	first := newReplayRegistry(NewRepository(env.runtime))
	fast := newReplayRegistry(NewRepository(env.runtime))
	slow := newReplayRegistry(NewRepository(env.runtime))
	claimed, err := first.claim(t.Context(), "consumed", now.Add(time.Minute), now)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = fast.claim(t.Context(), "other", now.Add(3*time.Minute), now.Add(2*time.Minute))
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = slow.claim(t.Context(), "consumed", now.Add(time.Minute), now)
	require.NoError(t, err)
	assert.False(t, claimed, "a fast replica must not remove a claim still live on the shared clock")

	claimed, err = slow.claim(t.Context(), "expired-before-insert", now.Add(-time.Minute), now.Add(-2*time.Minute))
	require.NoError(t, err)
	assert.False(t, claimed, "a slow or paused caller cannot consume a message already expired on the database clock")
}

func TestReplayRegistryStoreFailuresDenyConsumption(t *testing.T) {
	env := newFederationEnvironment(t)
	now := time.Now().UTC()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	claimed, err := env.service.replays.claim(ctx, "canceled", now.Add(time.Minute), now)
	assert.False(t, claimed)
	assert.ErrorIs(t, err, errReplayUnavailable)
	assert.ErrorIs(t, err, context.Canceled)
	require.NoError(t, env.db.Exec("DROP TABLE iam_federation_replay_claims").Error)
	claimed, err = newReplayRegistry(NewRepository(env.runtime)).claim(t.Context(), "missing-table", now.Add(time.Minute), now)
	assert.False(t, claimed)
	assert.ErrorIs(t, err, errReplayUnavailable)
}

func TestReplayRegistryClockFailureAfterInsertDeniesConsumption(t *testing.T) {
	env := newFederationEnvironment(t)
	now := time.Now().UTC()
	registry := env.service.replays
	registry.sweptAt = now
	clockErr := errors.New("clock unavailable")
	const callback = "test:replay-clock-failure"
	require.NoError(t, env.db.Callback().Row().Before("gorm:row").Register(callback, func(db *gorm.DB) {
		db.AddError(clockErr)
	}))
	claimed, err := registry.claim(t.Context(), "committed-before-clock-failure", now.Add(time.Minute), now)
	assert.False(t, claimed)
	assert.ErrorIs(t, err, errReplayUnavailable)
	assert.ErrorIs(t, err, clockErr)
	require.NoError(t, env.db.Callback().Row().Remove(callback))
	var count int64
	require.NoError(t, env.db.Table("iam_federation_replay_claims").Count(&count).Error)
	assert.EqualValues(t, 1, count, "a failed clock read must not release a committed claim")
	claimed, err = newReplayRegistry(NewRepository(env.runtime)).claim(t.Context(), "committed-before-clock-failure", now.Add(time.Minute), now)
	assert.NoError(t, err)
	assert.False(t, claimed)
}

func TestReplayRegistryCleanupFailureDoesNotPreventAtomicClaim(t *testing.T) {
	env := newFederationEnvironment(t)
	now := time.Now().UTC()
	const callback = "test:replay-cleanup-failure"
	require.NoError(t, env.db.Callback().Delete().Before("gorm:delete").Register(callback, func(db *gorm.DB) {
		db.AddError(errors.New("cleanup unavailable"))
	}))
	t.Cleanup(func() { require.NoError(t, env.db.Callback().Delete().Remove(callback)) })
	claimed, err := env.service.replays.claim(t.Context(), "cleanup-failed", now.Add(time.Minute), now)
	require.NoError(t, err)
	assert.True(t, claimed)
	claimed, err = newReplayRegistry(NewRepository(env.runtime)).claim(t.Context(), "cleanup-failed", now.Add(time.Minute), now)
	require.NoError(t, err)
	assert.False(t, claimed)
}

// The integration harness supplies an isolated database. Separate connection
// pools exercise the same SQL and atomic claims used by deployed replicas.
func TestReplayRegistryExternalDatabase(t *testing.T) {
	dialect, dsn := os.Getenv("IAM_LIFECYCLE_DATABASE_TYPE"), os.Getenv("IAM_LIFECYCLE_DATABASE_DSN")
	if dialect == "" && dsn == "" {
		t.Skip("external database is provided by the lifecycle and real MySQL gates")
	}
	require.NotEmpty(t, dialect)
	require.NotEmpty(t, dsn)
	prefix := os.Getenv("IAM_LIFECYCLE_TABLE_PREFIX") + "replay_"
	open := func() *Repository {
		router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
			"primary": {Type: dialect, DSN: dsn, Writable: true, Readable: true},
		})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, router.Close()) })
		return NewRepository(appenv.NewRuntime(appenv.NewIdentity("replay-test", "1"),
			appenv.WithDatabase(router), appenv.WithTablePrefix(prefix)))
	}
	first, second := open(), open()
	sqlDB, err := first.writer().DB()
	require.NoError(t, err)
	for _, source := range []goosex.Source{(&identity.Module{}).MigrationSource(), (&Module{}).MigrationSource()} {
		source.Prefix = prefix
		require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, dialect))
	}
	now := time.Now().UTC()
	registries := []*replayRegistry{newReplayRegistry(first), newReplayRegistry(second)}
	var group sync.WaitGroup
	var winners atomic.Int32
	for i := range 32 {
		group.Go(func() {
			claimed, err := registries[i%2].claim(t.Context(), "concurrent", now.Add(time.Minute), now)
			assert.NoError(t, err)
			if claimed {
				winners.Add(1)
			}
		})
	}
	group.Wait()
	require.EqualValues(t, 1, winners.Load())
	claimed, err := newReplayRegistry(open()).claim(t.Context(), "concurrent", now.Add(time.Minute), now)
	require.NoError(t, err)
	assert.False(t, claimed, "a new connection pool must retain consumption history")
	claimed, err = newReplayRegistry(second).claim(t.Context(), "fast-replica", now.Add(3*time.Minute), now.Add(2*time.Minute))
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = newReplayRegistry(first).claim(t.Context(), "concurrent", now.Add(time.Minute), now)
	require.NoError(t, err)
	assert.False(t, claimed, "a fast replica must not erase live claims")
	claimed, err = newReplayRegistry(second).claim(t.Context(), "slow-expired", now.Add(-time.Minute), now.Add(-2*time.Minute))
	require.NoError(t, err)
	assert.False(t, claimed, "the database clock must reject an expired request")
	for _, key := range []string{"Case", "case", "case ", strings.Repeat("a", 4096)} {
		claimed, err = registries[0].claim(t.Context(), key, now.Add(time.Minute), now)
		require.NoError(t, err)
		require.True(t, claimed)
		claimed, err = registries[1].claim(t.Context(), key, now.Add(time.Minute), now)
		require.NoError(t, err)
		assert.False(t, claimed)
	}
	// Only expired rows are removed by the real engine's clock expression.
	require.NoError(t, second.sweepReplayClaims(t.Context()))
	var count int64
	require.NoError(t, first.writer().Table(prefix+"iam_federation_replay_claims").Count(&count).Error)
	assert.EqualValues(t, 6, count)
}

func TestFederationOIDCReplayIsSharedAndStoreFailureDoesNotIssueSession(t *testing.T) {
	env := newFederationEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("idp", env.idp.issuer))
	_, state, cookie, callback := env.completeOIDC(t, provider)
	env.service.replays = newReplayRegistry(NewRepository(env.runtime))
	_, err := env.service.CompleteLogin(t.Context(), provider.ID, "unused-code", state.Nonce, stateCookieName+"="+cookie, callback)
	assert.ErrorIs(t, err, ErrOIDCState)

	start, fresh, callback := env.startOIDC(t, provider)
	require.NoError(t, env.db.Exec("DROP TABLE iam_federation_replay_claims").Error)
	result, err := env.service.CompleteLogin(t.Context(), provider.ID, "unused-code", fresh.Nonce, start.StateCookie, callback)
	assert.ErrorIs(t, err, errReplayUnavailable)
	assert.Empty(t, result.SessionToken)
}
