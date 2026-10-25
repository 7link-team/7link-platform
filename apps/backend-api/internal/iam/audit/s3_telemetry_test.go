package audit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestAuditS3TelemetryObservesRealHTTPFailureRecoveryAndCancellation(t *testing.T) {
	var status atomic.Int64
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
		if r.URL.Query().Has("location") {
			_, _ = w.Write([]byte(`<LocationConstraint>us-east-1</LocationConstraint>`))
		}
	}))
	defer server.Close()
	store, err := NewAnchorStore(AnchorConfig{Enabled: true, Endpoint: server.URL, Bucket: "audit-records", AccessKey: "private-access", SecretKey: "private-secret", RetentionDays: 30})
	require.NoError(t, err)
	m := &Module{service: &Service{archive: store}}
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(m.Collector()))
	families, err := registry.Gather()
	require.NoError(t, err)
	require.Len(t, families, 2, "availability is unknown until an actual S3 attempt")
	// The real SDK client created by NewAnchorStore must use the observed transport.
	_, err = store.client.BucketExists(t.Context(), "audit-records")
	require.NoError(t, err)
	require.InDelta(t, 1, metricValue(t, store.telemetry.available.WithLabelValues()), 0)
	request := func(ctx context.Context) {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, server.URL+"/audit-records", nil)
		require.NoError(t, err)
		resp, _ := store.telemetry.RoundTrip(req)
		if resp != nil {
			require.NoError(t, resp.Body.Close())
		}
	}
	status.Store(http.StatusServiceUnavailable)
	request(t.Context())
	require.InDelta(t, 0, metricValue(t, store.telemetry.available.WithLabelValues()), 0)
	require.InDelta(t, 1, metricValue(t, store.telemetry.requests.WithLabelValues("HEAD", "failed")), 0)
	status.Store(http.StatusNotFound)
	request(t.Context())
	require.InDelta(t, 1, metricValue(t, store.telemetry.available.WithLabelValues()), 0, "a missing object proves service reachability")
	status.Store(http.StatusForbidden)
	request(t.Context())
	require.InDelta(t, 0, metricValue(t, store.telemetry.available.WithLabelValues()), 0, "unusable credentials do not count as available")
	status.Store(http.StatusNoContent)
	request(t.Context())
	require.InDelta(t, 1, metricValue(t, store.telemetry.available.WithLabelValues()), 0)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request(ctx)
	require.InDelta(t, 1, metricValue(t, store.telemetry.available.WithLabelValues()), 0, "caller cancellation is not an outage")
	families, err = registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				require.NotContains(t, label.GetValue(), "private")
				require.NotContains(t, label.GetValue(), "audit-records")
			}
		}
	}
	server.Close()
	request(t.Context())
	require.InDelta(t, 0, metricValue(t, store.telemetry.available.WithLabelValues()), 0)
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

func TestAuditS3CollectorCombinesEveryObservedDistinctStore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/failed" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	first, err := newS3Telemetry(false)
	require.NoError(t, err)
	second, err := newS3Telemetry(false)
	require.NoError(t, err)
	for _, item := range []struct {
		telemetry *s3Telemetry
		path      string
	}{{first, "/healthy"}, {second, "/failed"}} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodHead, server.URL+item.path, nil)
		require.NoError(t, err)
		resp, err := item.telemetry.RoundTrip(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	shared := &AnchorStore{telemetry: first}
	m := &Module{service: &Service{anchor: shared, archive: &AnchorStore{telemetry: second}, erasureLedger: &ErasureLedger{store: shared}}}
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(m.Collector()))
	families, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() == "iam_audit_s3_available" {
			require.InDelta(t, 0, family.Metric[0].GetGauge().GetValue(), 0)
		}
		if family.GetName() == "iam_audit_s3_requests_total" {
			var total float64
			for _, metric := range family.Metric {
				total += metric.GetCounter().GetValue()
			}
			require.InDelta(t, 2, total, 0, "shared transports are counted once; independent failing transports remain visible")
		}
	}
}
