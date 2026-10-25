package realtime

import (
	"context"
	"strings"
	"testing"

	"7link.com/platform/pkg/appenv"
	"github.com/stretchr/testify/require"
)

func TestBroadcastRejectsInvalidOversizedOrStoppedRequests(t *testing.T) {
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
	require.ErrorIs(t, m.Broadcast(t.Context(), "tenant/1/projects", nil), ErrUnavailable)
	m.hub = activeHub()
	require.ErrorIs(t, m.Broadcast(t.Context(), "invalid", nil), ErrInvalid)
	require.ErrorIs(t, m.Broadcast(t.Context(), "tenant/1/projects", strings.Repeat("a", 4096)), ErrInvalid)
	require.ErrorIs(t, m.Broadcast(t.Context(), "tenant/1/projects", make(chan int)), ErrInvalid)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, m.Broadcast(ctx, "tenant/1/projects", nil), context.Canceled)
	require.ErrorIs(t, m.Broadcast(t.Context(), "tenant/1/projects", Message{Type: "bad\nevent"}), ErrInvalid)
	require.NoError(t, m.Broadcast(t.Context(), "tenant/1/projects", nil))
	m.flushBroadcasts(t.Context())
	require.Empty(t, m.pending)
	m.flushBroadcasts(t.Context())
	require.NoError(t, m.Quiesce(t.Context()))
	require.ErrorIs(t, m.Broadcast(t.Context(), "tenant/1/projects", nil), ErrUnavailable)
}
