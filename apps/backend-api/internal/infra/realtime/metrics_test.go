package realtime

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRealtimeCollectorTracksConnectionsResyncAndShutdown(t *testing.T) {
	h := activeHub()
	unknown := int64(1)
	c, _, _, err := h.connect(1, []string{"tenant/1/inbox/1"}, &unknown)
	require.NoError(t, err)
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(h))
	metrics, err := registry.Gather()
	require.NoError(t, err)
	found := false
	for _, metric := range metrics {
		if metric.GetName() == "realtime_streams" {
			require.InDelta(t, float64(1), metric.Metric[0].GetGauge().GetValue(), 0)
			found = true
		}
	}
	require.True(t, found)
	require.NoError(t, h.quiesce(t.Context()))
	h.disconnect(c)
	metrics, err = registry.Gather()
	require.NoError(t, err)
	foundNames := map[string]bool{}
	for _, metric := range metrics {
		foundNames[metric.GetName()] = true
		if metric.GetName() == "realtime_streams" {
			require.Zero(t, metric.Metric[0].GetGauge().GetValue())
		}
	}
	require.True(t, foundNames["realtime_resync_total"])
	require.True(t, foundNames["realtime_dropped_total"])
	require.True(t, foundNames["realtime_delivery_latency_seconds"])
}
