package realtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/events"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func activeHub() *Hub {
	h := newHub()
	h.cancel = func() {}
	h.done = make(chan struct{})
	close(h.done)
	return h
}
func signalEvent(seq int64, topics ...string) events.StreamEvent {
	return events.StreamEvent{Seq: seq, Type: "work.changed", Topics: topics, Mode: events.RealtimeSignal, SequencedAt: time.Now(), OccurredAt: time.Now()}
}

func TestHubReplayDeduplicatesTopicsAndIsolatesSlowConsumers(t *testing.T) {
	h := activeHub()
	a, b := "tenant/1/project/2", "tenant/1/work_item/3"
	c, _, _, err := h.connect(10, []string{a, b}, nil)
	require.NoError(t, err)
	e := signalEvent(1, a, b)
	h.deliver(e)
	require.Len(t, c.queue, 1)
	require.EqualValues(t, 1, (<-c.queue).Seq)
	zero := int64(0)
	replayed, replay, reason, err := h.connect(11, []string{a, b}, &zero)
	require.NoError(t, err)
	require.Empty(t, reason)
	require.Len(t, replay, 1)
	fast, _, _, err := h.connect(12, []string{a}, nil)
	require.NoError(t, err)
	for seq := int64(2); seq <= 258; seq++ {
		h.deliver(signalEvent(seq, a))
		require.Equal(t, seq, (<-fast.queue).Seq)
	}
	require.Equal(t, "slow_consumer", <-c.stop)
	require.Equal(t, "slow_consumer", <-replayed.stop)
	registry := prometheus.NewRegistry()
	registry.MustRegister(h.dropped)
	metrics, err := registry.Gather()
	require.NoError(t, err)
	require.InDelta(t, float64(2), metrics[0].Metric[0].GetCounter().GetValue(), 0)
	h.revoke(fast, a)
	h.deliver(signalEvent(259, a))
	require.Empty(t, fast.queue)
	h.disconnect(c)
	h.disconnect(c)
	h.disconnect(replayed)
	h.disconnect(fast)
	require.Empty(t, h.connections)
	require.Empty(t, h.principals)
	require.Empty(t, h.byTopic)
}
func TestHubCursorBoundsRetentionLimitsAndInternalCoalescing(t *testing.T) {
	h := activeHub()
	topic := "tenant/1/machine/2/channel"
	ctx, cancel := context.WithCancel(t.Context())
	signals, stop := h.Subscribe(ctx, []string{topic, "invalid"})
	defer stop()
	h.deliver(signalEvent(1, topic))
	h.deliver(signalEvent(2, topic))
	require.Len(t, signals, 1)
	<-signals
	cancel()
	require.Eventually(t, func() bool { _, open := <-signals; return !open }, time.Second, time.Millisecond)
	old := signalEvent(3, topic)
	old.SequencedAt = time.Now().Add(-16 * time.Minute)
	for i := range h.ring {
		h.ring[i].SequencedAt = old.SequencedAt
	}
	h.deliver(old)
	h.deliver(signalEvent(4, topic))
	zero, unknown := int64(0), int64(999)
	c, _, reason, err := h.connect(1, []string{topic}, &zero)
	require.NoError(t, err)
	require.Equal(t, "cursor_too_old", reason)
	h.disconnect(c)
	c, _, reason, err = h.connect(1, []string{topic}, &unknown)
	require.NoError(t, err)
	require.Equal(t, "cursor_unknown", reason)
	h.disconnect(c)
	for i := 0; i < 10; i++ {
		_, _, _, err = h.connect(1, []string{topic}, nil)
		require.NoError(t, err)
	}
	_, _, _, err = h.connect(1, []string{topic}, nil)
	require.ErrorIs(t, err, ErrLimit)
	h.principals[1] = 0
	for len(h.connections) < 5000 {
		h.connections[&connection{}] = true
	}
	_, _, _, err = h.connect(2, nil, nil)
	require.ErrorIs(t, err, ErrLimit)
	// Avoid synthetic quota-only connections in the lifecycle test below.
	h.connections = map[*connection]bool{}
	require.NoError(t, h.quiesce(t.Context()))
	_, _, _, err = h.connect(2, nil, nil)
	require.ErrorIs(t, err, ErrUnavailable)
	s, cleanup := h.Subscribe(t.Context(), []string{topic})
	cleanup()
	_, open := <-s
	require.False(t, open)
}
func TestHubWarmupTailAndQuiesce(t *testing.T) {
	h := newHub()
	topic := "tenant/1/projects"
	warm := signalEvent(1, topic)
	obsolete := signalEvent(0, topic)
	obsolete.SequencedAt = time.Now().Add(-time.Hour)
	tail := func(_ context.Context, after int64, _ int, newest bool) (events.TailPage, error) {
		if newest {
			return events.TailPage{HighWater: 1, Events: []events.StreamEvent{obsolete, warm}}, nil
		}
		if after == 1 {
			return events.TailPage{HighWater: 2, Events: []events.StreamEvent{signalEvent(2, topic)}}, nil
		}
		return events.TailPage{HighWater: 2}, nil
	}
	require.NoError(t, h.start(t.Context(), tail, func(context.Context) {}))
	require.NoError(t, h.start(t.Context(), tail, nil))
	zero := int64(0)
	c, replay, _, err := h.connect(2, []string{topic}, &zero)
	require.NoError(t, err)
	require.Len(t, replay, 1)
	select {
	case e := <-c.queue:
		require.EqualValues(t, 2, e.Seq)
	case <-time.After(time.Second):
		t.Fatal("replica did not tail")
	}
	require.NoError(t, h.quiesce(t.Context()))
	require.Equal(t, "reconnect", <-c.stop)
	h.disconnect(c)
	require.ErrorIs(t, h.start(t.Context(), tail, nil), ErrUnavailable)
}

func TestHubNotificationWakeupAndNonRealtimeEvents(t *testing.T) {
	h := newHub()
	wake := make(chan struct{}, 1)
	var phase atomic.Int64
	tail := func(context.Context, int64, int, bool) (events.TailPage, error) {
		if phase.Load() == 0 {
			return events.TailPage{}, nil
		}
		return events.TailPage{HighWater: 1, Events: []events.StreamEvent{signalEvent(1, "tenant/1/projects")}}, nil
	}
	require.NoError(t, h.start(t.Context(), tail, func(context.Context) {}, func(context.Context) (<-chan struct{}, error) { return wake, nil }))
	defer h.quiesce(context.Background())
	c, _, _, err := h.connect(1, []string{"tenant/1/projects"}, nil)
	require.NoError(t, err)
	defer h.disconnect(c)
	phase.Store(1)
	wake <- struct{}{}
	select {
	case e := <-c.queue:
		require.EqualValues(t, 1, e.Seq)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("notification did not wake tail before poll")
	}
	close(wake)
	none := signalEvent(2, "tenant/1/projects")
	none.Mode = events.RealtimeNone
	h.deliver(none)
	require.Empty(t, c.queue)
	require.False(t, matches(c.topics, none))
	require.False(t, matches(c.topics, signalEvent(3, "tenant/2/projects")))
	h.deliver(none)
	require.Empty(t, c.queue)
}
