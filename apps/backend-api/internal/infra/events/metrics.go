package events

import (
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/prometheus/client_golang/prometheus"
	"strconv"
	"time"
)

type eventMetrics struct {
	published        *prometheus.CounterVec
	unsequenced, lag prometheus.Gauge
	dispatch         *prometheus.GaugeVec
	duration         *prometheus.HistogramVec
	failures, parked *prometheus.CounterVec
}

func newEventMetrics() *eventMetrics {
	return &eventMetrics{
		published:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "domain_events_published_total", Help: "Committed events assigned a stream sequence."}, []string{"event_type"}),
		unsequenced: prometheus.NewGauge(prometheus.GaugeOpts{Name: "domain_events_unsequenced", Help: "Committed events awaiting sequencing."}),
		lag:         prometheus.NewGauge(prometheus.GaugeOpts{Name: "domain_events_sequencer_lag_seconds", Help: "Age of oldest unsequenced event."}),
		dispatch:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "domain_events_dispatch_lag", Help: "Sequence distance behind sequencer."}, []string{"subscriber", "partition"}),
		duration:    prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "domain_events_handler_duration_seconds", Help: "Subscriber handler execution time.", Buckets: prometheus.DefBuckets}, []string{"subscriber"}),
		failures:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "domain_events_handler_failures_total", Help: "Subscriber handler failures."}, []string{"subscriber"}),
		parked:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "domain_events_parked_total", Help: "New FAILED parked events."}, []string{"subscriber"}),
	}
}
func (m *eventMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{m.published, m.unsequenced, m.lag, m.dispatch, m.duration, m.failures, m.parked}
}
func (m *Module) Collector() prometheus.Collector { return m }
func (m *Module) Describe(ch chan<- *prometheus.Desc) {
	for _, collector := range m.metrics.collectors() {
		collector.Describe(ch)
	}
}
func (m *Module) Collect(ch chan<- prometheus.Metric) {
	if db := m.writer(); db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		q := query.BindWithNames(db, m.names)
		if count, err := q.WithContext(ctx).Where(q.StreamSeq.IsNull()).Count(); err == nil {
			m.metrics.unsequenced.Set(float64(count))
			m.metrics.lag.Set(0)
			if count > 0 {
				if first, e := q.WithContext(ctx).Where(q.StreamSeq.IsNull()).Order(q.OccurredAt).Take(); e == nil {
					m.metrics.lag.Set(max(0, m.now().Sub(first.OccurredAt).Seconds()))
				}
			}
		}
		if statuses, err := m.SubscribersOn(ctx, db); err == nil {
			for _, s := range statuses {
				for _, c := range s.Cursors {
					m.metrics.dispatch.WithLabelValues(s.Name, strconv.Itoa(c.Partition)).Set(float64(c.Lag))
				}
			}
		}
	}
	for _, collector := range m.metrics.collectors() {
		collector.Collect(ch)
	}
}

var _ module.PrometheusCollectorModule = (*Module)(nil)
