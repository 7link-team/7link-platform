package jobs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestTimedOutHandlerRetainsSlotAndExportsLeakUntilReturn(t *testing.T) {
	m, db := queueHarness(t, 10)
	claim := runningJob(t, m)
	claim.TimeoutMS = 20
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	h := m.handlers[claim.Kind]
	h.Run = func(context.Context, Job) error { <-release; return nil }
	m.handlers[claim.Kind] = h
	done := make(chan error, 1)
	go func() { _, err := m.executeClaim(t.Context(), claim, nil); done <- err }()
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(m.Collector()))
	m.leakedWorkers.WithLabelValues(claim.Queue).Set(0)
	count := func() float64 {
		metrics, err := registry.Gather()
		require.NoError(t, err)
		require.Len(t, metrics, 1)
		return metrics[0].Metric[0].Gauge.GetValue()
	}
	require.Eventually(t, func() bool { return count() == 1 }, 35*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool { return storedJob(t, db, claim.ID).LastErrorCode == "TIMEOUT" }, time.Second, 10*time.Millisecond)
	select {
	case <-done:
		t.Fatal("leaked worker released slot")
	default:
	}
	metrics, err := registry.Gather()
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Equal(t, "queue", metrics[0].Metric[0].Label[0].GetName())
	require.Equal(t, claim.Queue, metrics[0].Metric[0].Label[0].GetValue())
	require.Equal(t, "jobs_leaked_workers", metrics[0].GetName())
	require.InDelta(t, 1, metrics[0].Metric[0].Gauge.GetValue(), 0)
	version := storedJob(t, db, claim.ID).Version
	unblock()
	require.NoError(t, <-done)
	require.Zero(t, count())
	require.Equal(t, version, storedJob(t, db, claim.ID).Version)
}
