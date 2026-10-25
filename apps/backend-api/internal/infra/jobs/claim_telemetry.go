package jobs

import (
	"context"
	"errors"

	"7link.com/platform/internal/infra/jobs/model"
	"github.com/prometheus/client_golang/prometheus"
)

type claimTelemetry struct {
	success *prometheus.GaugeVec
	errors  *prometheus.CounterVec
}

func (m *Module) withClaimTelemetry() *Module {
	m.claimStats = &claimTelemetry{
		success: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "jobs_claim_success", Help: "Whether the last actual queue claim poll succeeded; empty successful polls are healthy. No sample before the first poll."}, []string{"queue"}),
		errors:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "jobs_claim_errors_total", Help: "Actual failed queue claim polls, excluding worker shutdown cancellation."}, []string{"queue"}),
	}
	return m
}

// claimObserved changes no queue semantics and observes the same result that the
// worker logs. A healthy empty poll resets a failure episode immediately.
func (m *Module) claimObserved(ctx context.Context, queue, owner string, slots int) ([]model.Job, error) {
	rows, err := m.claim(ctx, queue, owner, slots)
	if m.claimStats != nil && !errors.Is(ctx.Err(), context.Canceled) {
		value := float64(1)
		if err != nil {
			value = 0
			m.claimStats.errors.WithLabelValues(queue).Inc()
		}
		m.claimStats.success.WithLabelValues(queue).Set(value)
	}
	return rows, err
}

type claimCollector struct{ m *Module }

func (c claimCollector) Describe(ch chan<- *prometheus.Desc) {
	c.m.leakedWorkers.Describe(ch)
	if t := c.m.claimStats; t != nil {
		t.success.Describe(ch)
		t.errors.Describe(ch)
	}
}
func (c claimCollector) Collect(ch chan<- prometheus.Metric) {
	c.m.leakedWorkers.Collect(ch)
	if t := c.m.claimStats; t != nil {
		t.success.Collect(ch)
		t.errors.Collect(ch)
	}
}
