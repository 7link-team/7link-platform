package webhook

import (
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatherDeliveryMetrics scrapes the module the way the metrics registry does, and
// records each value under its name and type so a counter exported as a gauge,
// or the reverse, fails here rather than in a rate() that silently misbehaves.
func gatherDeliveryMetrics(t *testing.T, m *Module) map[string]float64 {
	t.Helper()
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(m.Collector()))
	families, err := registry.Gather()
	require.NoError(t, err)
	values := map[string]float64{}
	for _, family := range families {
		require.Len(t, family.Metric, 1)
		switch family.GetType().String() {
		case "COUNTER":
			values["counter "+family.GetName()] = family.Metric[0].GetCounter().GetValue()
		case "GAUGE":
			values["gauge "+family.GetName()] = family.Metric[0].GetGauge().GetValue()
		default:
			t.Fatalf("unexpected metric type for %s", family.GetName())
		}
	}
	return values
}

func TestDeliveryMetricsWithoutAServiceReportNothingObserved(t *testing.T) {
	assert.Equal(t, map[string]float64{
		"counter iam_webhook_delivered_total":                 0,
		"counter iam_webhook_delivery_failures_total":         0,
		"counter iam_webhook_deliveries_abandoned_total":      0,
		"counter iam_webhook_delivery_leases_reclaimed_total": 0,
		"gauge iam_webhook_deliveries_backlog":                0,
		"gauge iam_webhook_metrics_database_success":          0,
	}, gatherDeliveryMetrics(t, &Module{}), "zero backlog is only believable when the probe says it read it")
}

func TestDeliveryMetricsFollowTheWorkerOutcomes(t *testing.T) {
	env := newWebhookEnvironment(t)
	instance := &Module{service: env.service}
	refusing := newReceiver(t)
	refusing.status.Store(http.StatusInternalServerError)
	env.subscribe(refusing)
	accepting := newReceiver(t)
	env.subscribe(accepting)
	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))

	values := gatherDeliveryMetrics(t, instance)
	assert.InDelta(t, 2, values["gauge iam_webhook_deliveries_backlog"], 0, "both deliveries are due")
	assert.InDelta(t, 1, values["gauge iam_webhook_metrics_database_success"], 0)

	require.NoError(t, env.service.DeliverDue(t.Context()))
	values = gatherDeliveryMetrics(t, instance)
	assert.InDelta(t, 1, values["counter iam_webhook_delivered_total"], 0)
	assert.InDelta(t, 1, values["counter iam_webhook_delivery_failures_total"], 0)
	assert.Zero(t, values["counter iam_webhook_deliveries_abandoned_total"])
	assert.Zero(t, values["gauge iam_webhook_deliveries_backlog"], "a retry waiting out its backoff is not due yet")

	// MaxAttempts is 3: two more refusals spend them.
	for attempt := 2; attempt <= 3; attempt++ {
		env.service.now = func() time.Time { return webhookTestNow.Add(time.Duration(attempt) * time.Hour) }
		require.NoError(t, env.service.DeliverDue(t.Context()))
	}
	values = gatherDeliveryMetrics(t, instance)
	assert.InDelta(t, 3, values["counter iam_webhook_delivery_failures_total"], 0)
	assert.InDelta(t, 1, values["counter iam_webhook_deliveries_abandoned_total"], 0)
	assert.InDelta(t, 1, values["counter iam_webhook_delivered_total"], 0)
}

func TestDeliveryMetricsCountAClaimAsBacklogUntilItIsReclaimed(t *testing.T) {
	env := newWebhookEnvironment(t)
	instance := &Module{service: env.service}
	target := newReceiver(t)
	item := env.subscribe(target)
	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))
	claimed, err := env.service.repo.claimDelivery(t.Context(), env.deliveries(item.ID)[0].ID, 0, webhookTestNow)
	require.NoError(t, err)
	require.True(t, claimed)

	values := gatherDeliveryMetrics(t, instance)
	assert.InDelta(t, 1, values["gauge iam_webhook_deliveries_backlog"], 0, "a claim without an outcome is still owed")
	assert.Zero(t, values["counter iam_webhook_delivery_leases_reclaimed_total"])

	env.service.now = func() time.Time { return webhookTestNow.Add(time.Hour) }
	require.NoError(t, env.service.DeliverDue(t.Context()))
	values = gatherDeliveryMetrics(t, instance)
	assert.InDelta(t, 1, values["counter iam_webhook_delivery_leases_reclaimed_total"], 0)
	assert.InDelta(t, 1, values["counter iam_webhook_delivered_total"], 0)
	assert.Zero(t, values["gauge iam_webhook_deliveries_backlog"])
}

func TestDeliveryMetricsSayWhenTheBacklogCannotBeRead(t *testing.T) {
	env := newWebhookEnvironment(t)
	require.NoError(t, env.db.Exec("DROP TABLE iam_webhook_deliveries").Error)
	values := gatherDeliveryMetrics(t, &Module{service: env.service})
	assert.Zero(t, values["gauge iam_webhook_metrics_database_success"])
	assert.Zero(t, values["gauge iam_webhook_deliveries_backlog"])
}
