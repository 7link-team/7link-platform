package webhook

import (
	"context"
	"time"

	"7link.com/platform/pkg/module"
	"github.com/prometheus/client_golang/prometheus"
)

// metricsProbeTimeout bounds the backlog count a scrape runs, so a slow database
// delays one scrape rather than every one queued behind it.
const metricsProbeTimeout = 2 * time.Second

var deliveryMetricDescriptions = []*prometheus.Desc{
	prometheus.NewDesc("iam_webhook_delivered_total", "Webhook deliveries a receiver accepted.", nil, nil),
	prometheus.NewDesc("iam_webhook_delivery_failures_total", "Webhook delivery attempts that failed, whether or not they will be retried.", nil, nil),
	prometheus.NewDesc("iam_webhook_deliveries_abandoned_total", "Webhook deliveries that reached the terminal failed state and will not be retried.", nil, nil),
	prometheus.NewDesc("iam_webhook_delivery_leases_reclaimed_total", "Webhook delivery claims taken back after their lease expired without an outcome.", nil, nil),
	prometheus.NewDesc("iam_webhook_deliveries_backlog", "Webhook deliveries that are due or claimed and not yet resolved.", nil, nil),
	prometheus.NewDesc("iam_webhook_metrics_database_success", "Whether the webhook delivery backlog could be read for this scrape.", nil, nil),
}

// Collector exposes the delivery worker's outcomes. The worker has no caller to
// report to, so without these a receiver refusing everything shows up only in
// the log and in each tenant's delivery history.
func (m *Module) Collector() prometheus.Collector { return m }

func (m *Module) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range deliveryMetricDescriptions {
		ch <- desc
	}
}

func (m *Module) Collect(ch chan<- prometheus.Metric) {
	values := make([]float64, len(deliveryMetricDescriptions))
	if service := m.service; service != nil {
		values[0] = float64(service.delivered.Load())
		values[1] = float64(service.failures.Load())
		values[2] = float64(service.abandoned.Load())
		values[3] = float64(service.reclaimed.Load())
		ctx, cancel := context.WithTimeout(context.Background(), metricsProbeTimeout)
		defer cancel()
		if backlog, err := service.repo.deliveryBacklog(ctx, service.now().UTC()); err == nil {
			values[4], values[5] = float64(backlog), 1
		}
	}
	for i, desc := range deliveryMetricDescriptions {
		kind := prometheus.CounterValue
		if i >= 4 {
			kind = prometheus.GaugeValue
		}
		ch <- prometheus.MustNewConstMetric(desc, kind, values[i])
	}
}

var _ module.PrometheusCollectorModule = (*Module)(nil)
