package audit

import (
	"7link.com/platform/pkg/module"
	"github.com/prometheus/client_golang/prometheus"
)

var archiveMetricDescriptions = []*prometheus.Desc{
	prometheus.NewDesc("iam_audit_archive_sweep_failures_total", "Background audit archive sweeps that could not enumerate tenants.", nil, nil),
	prometheus.NewDesc("iam_audit_archive_attempt_failures_total", "Background audit archive requests or runs that failed; the job stays failed and retries with backoff.", nil, nil),
}

// Collector exposes background archive failures, which otherwise reach only
// the log because no caller observes the worker's result.
func (m *Module) Collector() prometheus.Collector { return m }

func (m *Module) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range archiveMetricDescriptions {
		ch <- desc
	}
}

func (m *Module) Collect(ch chan<- prometheus.Metric) {
	values := []float64{0, 0}
	if m.service != nil {
		values[0], values[1] = float64(m.service.archiveSweepFailures.Load()), float64(m.service.archiveAttemptFailures.Load())
	}
	for i, desc := range archiveMetricDescriptions {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, values[i])
	}
}

var _ module.PrometheusCollectorModule = (*Module)(nil)
