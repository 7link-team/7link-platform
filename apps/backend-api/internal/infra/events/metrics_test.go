package events

import (
	"7link.com/platform/pkg/appenv"
	"context"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestEventMetricsReflectCommittedSequencePendingLagAndDelivery(t *testing.T) {
	m, db := newTestSequencer(t)
	registerTestSubscriber(t, m, Subscriber{Name: "test.metrics", StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(context.Context, *gorm.DB, Envelope) error { return nil }})
	emitEvents(t, m, db, 2)
	_, err := m.DispatchPartition(t.Context(), "test.metrics", 0)
	require.NoError(t, err)
	originalNow := m.now
	m.now = func() time.Time { return originalNow().Add(-time.Minute) }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, x := m.Publish(t.Context(), tx, testEvent()); return x }))
	m.now = originalNow
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(m.Collector()))
	metrics, err := registry.Gather()
	require.NoError(t, err)
	found := map[string]bool{}
	for _, metric := range metrics {
		found[metric.GetName()] = true
		switch metric.GetName() {
		case "domain_events_published_total":
			require.InDelta(t, float64(2), metric.Metric[0].GetCounter().GetValue(), 0)
		case "domain_events_unsequenced":
			require.InDelta(t, float64(1), metric.Metric[0].GetGauge().GetValue(), 0)
		case "domain_events_sequencer_lag_seconds":
			require.InDelta(t, float64(60), metric.Metric[0].GetGauge().GetValue(), 0)
		case "domain_events_handler_duration_seconds":
			require.EqualValues(t, 2, metric.Metric[0].GetHistogram().GetSampleCount())
		}
	}
	require.True(t, found["domain_events_dispatch_lag"])
	_, err = m.Sequence(t.Context())
	require.NoError(t, err)
	metrics, err = registry.Gather()
	require.NoError(t, err)
	for _, metric := range metrics {
		if metric.GetName() == "domain_events_dispatch_lag" {
			require.InDelta(t, float64(1), metric.Metric[0].GetGauge().GetValue(), 0)
		}
	}
	failing := Subscriber{Name: "test.fail", Mode: AtLeastOnce, Handle: func(context.Context, *gorm.DB, Envelope) error { return context.Canceled }}
	require.Error(t, m.handle(t.Context(), nil, failing, Envelope{}))
	metrics, err = registry.Gather()
	require.NoError(t, err)
	for _, metric := range metrics {
		if metric.GetName() == "domain_events_handler_failures_total" {
			require.InDelta(t, float64(1), metric.Metric[0].GetCounter().GetValue(), 0)
		}
	}
	offline := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")))
	other := prometheus.NewRegistry()
	other.MustRegister(offline.Collector())
	_, err = other.Gather()
	require.NoError(t, err)
}
