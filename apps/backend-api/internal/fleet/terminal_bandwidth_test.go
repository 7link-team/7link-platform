package fleet

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTunnelBandwidthSharesBudgetAndDoesNotAccumulateIdleCredit(t *testing.T) {
	var limiter tunnelBandwidth
	now := time.Unix(100, 0)
	// One1024-byte frame at8kbps consumes one second. A second stream
	// must use the same connection budget rather than multiplying bandwidth.
	require.Zero(t, limiter.reserve(now, 1024, 8))
	require.Equal(t, time.Second, limiter.reserve(now, 1024, 8))
	require.Equal(t, 2*time.Second, limiter.reserve(now, 1024, 8))
	require.Zero(t, limiter.reserve(now.Add(time.Hour), 1024, 8))
	require.Equal(t, time.Second, limiter.reserve(now.Add(time.Hour), 1024, 8))
}

func TestTunnelMachineQuotaIsSharedAndReleaseIsIdempotent(t *testing.T) {
	m, _, ctx, _, _ := terminalFixture(t)
	m.terminalBroker = newTerminalBroker()
	limit := 16
	_, err := m.SetTerminalAccessOptions(ctx, 7, 1, 1, TerminalAccessOptions{Enabled: true, TunnelMaxStreams: &limit}, "browser", "fresh")
	require.NoError(t, err)
	var releases []func()
	for range limit {
		release, err := m.acquireTunnelStream(ctx, 7, 1)
		require.NoError(t, err)
		releases = append(releases, release)
	}
	_, err = m.acquireTunnelStream(ctx, 7, 1)
	require.ErrorIs(t, err, ErrTunnelQuota)
	releases[0]()
	releases[0]()
	replacement, err := m.acquireTunnelStream(ctx, 7, 1)
	require.NoError(t, err)
	_, err = m.acquireTunnelStream(ctx, 7, 1)
	require.ErrorIs(t, err, ErrTunnelQuota, "duplicate release must not create extra capacity")
	replacement()
	for _, release := range releases {
		release()
	}
	require.Zero(t, m.tunnelBudget(7, 1).streams)
}

func TestTunnelOutputThrottleUsesPersistedPolicyAndCancelsBoundedWait(t *testing.T) {
	m, db, ctx, _, _ := terminalFixture(t)
	m.terminalBroker = newTerminalBroker()
	preview := true
	_, e := m.SetTerminalAccessOptions(ctx, 7, 1, 1, TerminalAccessOptions{Enabled: true, AllowRemotePreview: &preview}, "browser", "fresh")
	require.NoError(t, e)
	done := make(chan struct{})
	require.NoError(t, m.throttleTunnelOutput(ctx, 7, 1, done, 0, "terminal"))
	require.NoError(t, m.throttleTunnelOutput(ctx, 7, 1, done, 1024, "terminal"))
	require.NoError(t, m.throttleTunnelOutput(ctx, 7, 1, done, 1024, "preview"))
	budget := m.tunnelBudget(7, 1)
	budget.mu.Lock()
	budget.next = time.Now().Add(time.Hour)
	budget.mu.Unlock()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, m.throttleTunnelOutput(canceled, 7, 1, done, 1024, "terminal"), context.Canceled)
	close(done)
	require.ErrorIs(t, m.throttleTunnelOutput(ctx, 7, 1, done, 1024, "preview"), ErrUnavailable)
	_, e = m.SetTerminalAccessOptions(ctx, 7, 1, 2, TerminalAccessOptions{Enabled: false}, "", "")
	require.NoError(t, e)
	require.ErrorIs(t, m.throttleTunnelOutput(ctx, 7, 1, done, 1, "terminal"), ErrTerminalAccessDisabled)
	injected := errors.New("quota storage unavailable")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("quota_source_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.TerminalAccess.String() {
			tx.AddError(injected)
		}
	}))
	require.ErrorIs(t, m.throttleTunnelOutput(ctx, 7, 1, done, 1, "preview"), injected)
	_, e = m.acquireTunnelStream(ctx, 7, 1)
	require.ErrorIs(t, e, injected)
	m.terminalBroker = nil
	_, e = m.acquireTunnelStream(ctx, 7, 1)
	require.ErrorIs(t, e, ErrUnavailable)
}

func TestTunnelOutputCurrentStorageAndTransportLossNeverRetainsQuota(t *testing.T) {
	for _, fault := range []string{"writer", "missing_access", "missing_broker", "expired_wait", "canceled_wait", "open_stale"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, row, _ := terminalFixture(t)
			m.terminalBroker = newTerminalBroker()
			_, e := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
			require.NoError(t, e)
			done := make(chan struct{})
			switch fault {
			case "writer":
				m.runtime = nil
			case "missing_access":
				require.NoError(t, db.Table(m.names.TerminalAccess.String()).Where("machine_id=?", 1).Delete(nil).Error)
			case "missing_broker":
				m.terminalBroker = nil
			case "expired_wait", "canceled_wait":
				budget := m.tunnelBudget(7, 1)
				budget.mu.Lock()
				budget.next = time.Now().Add(20 * time.Millisecond)
				budget.mu.Unlock()
				if fault == "canceled_wait" {
					waiting, cancel := context.WithCancel(ctx)
					result := make(chan error, 1)
					go func() {
						result <- m.throttleTerminalOutput(waiting, &terminalConnection{tenant: 7, machine: 1, done: done}, 1024)
					}()
					cancel()
					require.ErrorIs(t, <-result, context.Canceled)
					return
				}
			case "open_stale":
				_, e = m.openTerminalStream(ctx, terminalView(row), 80, 24)
				require.ErrorIs(t, e, ErrSessionStale)
				require.Zero(t, m.tunnelBudget(7, 1).streams)
				return
			}
			e = m.throttleTerminalOutput(ctx, &terminalConnection{tenant: 7, machine: 1, done: done}, 1024)
			if fault == "expired_wait" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
			if fault == "writer" || fault == "missing_access" {
				release, e := m.acquireTunnelStream(ctx, 7, 1)
				require.Error(t, e)
				require.Nil(t, release)
			}
		})
	}
}
