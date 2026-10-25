package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClaimTelemetryObservesRealFailureEmptyRecoveryAndShutdown(t *testing.T) {
	m, _ := queueHarness(t, 10)
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(m.Collector()))
	families, err := registry.Gather()
	require.NoError(t, err)
	require.Empty(t, families, "no invented poll result before an actual attempt")
	_, err = m.Enqueue(t.Context(), nil, EnqueueRequest{TenantID: 1, Kind: "jobs.work", IdempotencyKey: "telemetry-probe"})
	require.NoError(t, err)
	original := m.guard
	failed := errors.New("database unavailable")
	m.guard = func(context.Context, *gorm.DB, string) error { return failed }
	_, err = m.claimObserved(t.Context(), "default", "worker", 1)
	require.ErrorIs(t, err, failed)
	require.InDelta(t, 0, metricValue(t, m.claimStats.success.WithLabelValues("default")), 0)
	require.InDelta(t, 1, metricValue(t, m.claimStats.errors.WithLabelValues("default")), 0)
	m.guard = original
	_, err = m.Cancel(t.Context(), nil, 1, "jobs.work", "telemetry-probe")
	require.NoError(t, err)
	rows, err := m.claimObserved(t.Context(), "default", "worker", 1)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.InDelta(t, 1, metricValue(t, m.claimStats.success.WithLabelValues("default")), 0)
	require.InDelta(t, 1, metricValue(t, m.claimStats.errors.WithLabelValues("default")), 0)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _ = m.claimObserved(ctx, "default", "worker", 1)
	require.InDelta(t, 1, metricValue(t, m.claimStats.success.WithLabelValues("default")), 0)
	require.InDelta(t, 1, metricValue(t, m.claimStats.errors.WithLabelValues("default")), 0)
}

func metricValue(t *testing.T, metric prometheus.Metric) float64 {
	t.Helper()
	sample := &dto.Metric{}
	require.NoError(t, metric.Write(sample))
	if sample.Gauge != nil {
		return sample.Gauge.GetValue()
	}
	return sample.Counter.GetValue()
}
