package audit

import (
	"context"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gatherArchiveMetrics(t *testing.T, m *Module) map[string]int64 {
	t.Helper()
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(m.Collector()))
	families, err := registry.Gather()
	require.NoError(t, err)
	values := map[string]int64{}
	for _, family := range families {
		values[family.GetName()] = int64(family.Metric[0].GetCounter().GetValue())
	}
	return values
}

func TestArchiveMetricsWithoutService(t *testing.T) {
	values := gatherArchiveMetrics(t, &Module{})
	assert.Equal(t, map[string]int64{
		"iam_audit_archive_sweep_failures_total":   0,
		"iam_audit_archive_attempt_failures_total": 0,
	}, values)
}

func TestArchiveMetricsCountFailedBackgroundAttempts(t *testing.T) {
	h, fixture := archiveJobFixture(t)
	fixture.mu.Lock()
	fixture.failMethod, fixture.failContains = http.MethodGet, "retention"
	fixture.mu.Unlock()
	require.NoError(t, h.service.sweepArchives(t.Context(), time.Minute), "per-tenant failures do not abort the sweep")
	for _, tenantID := range []guid.ID{11, 12} {
		jobs, err := h.service.ListArchiveJobs(t.Context(), tenantID, 0, 20)
		require.NoError(t, err)
		require.Len(t, jobs, 1)
		require.Equal(t, "failed", jobs[0].State)
	}
	values := gatherArchiveMetrics(t, &Module{service: h.service})
	assert.EqualValues(t, 2, values["iam_audit_archive_attempt_failures_total"], "one failure per tenant job")
	assert.Zero(t, values["iam_audit_archive_sweep_failures_total"])

	// A job in backoff is busy, not failed again, so a repeated sweep is not recounted.
	require.NoError(t, h.service.sweepArchives(t.Context(), time.Minute))
	assert.EqualValues(t, 2, gatherArchiveMetrics(t, &Module{service: h.service})["iam_audit_archive_attempt_failures_total"])
}

func TestArchiveMetricsCountFailedSweeps(t *testing.T) {
	h, _ := archiveJobFixture(t)
	db, err := h.db.DB()
	require.NoError(t, err)
	require.NoError(t, db.Close())
	instance := &Module{service: h.service, config: Config{Archive: ArchiveConfig{Interval: time.Second, Timeout: time.Second}}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go instance.runArchives(ctx, h.service, done)
	require.Eventually(t, func() bool {
		return gatherArchiveMetrics(t, instance)["iam_audit_archive_sweep_failures_total"] >= 1
	}, 5*time.Second, 20*time.Millisecond)
	cancel()
	<-done
	assert.Zero(t, gatherArchiveMetrics(t, instance)["iam_audit_archive_attempt_failures_total"])
}
