package authn

import (
	"7link.com/platform/pkg/module"
	"github.com/prometheus/client_golang/prometheus"
)

var notificationMetricDescriptions = []*prometheus.Desc{
	prometheus.NewDesc("iam_authn_notification_poll_failures_total", "Notification outbox polls that could not read or reset pending deliveries.", nil, nil),
	prometheus.NewDesc("iam_authn_notification_retryable_failures_total", "Notification delivery attempts that failed and were rescheduled.", nil, nil),
	prometheus.NewDesc("iam_authn_notification_terminal_failures_total", "Notifications that reached the terminal failed state and will not be delivered.", nil, nil),
}

// Collector exposes notification outbox failures, which otherwise reach only
// the log because the worker has no caller to report to.
func (m *Module) Collector() prometheus.Collector { return m }

func (m *Module) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range notificationMetricDescriptions {
		ch <- desc
	}
}

func (m *Module) Collect(ch chan<- prometheus.Metric) {
	values := []float64{0, 0, 0}
	if m.Service != nil {
		values[0] = float64(m.notificationPollFailures.Load())
		values[1] = float64(m.notificationRetryableFailures.Load())
		values[2] = float64(m.notificationTerminalFailures.Load())
	}
	for i, desc := range notificationMetricDescriptions {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, values[i])
	}
}

var _ module.PrometheusCollectorModule = (*Module)(nil)
