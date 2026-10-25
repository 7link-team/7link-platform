package jobs

import (
	"7link.com/platform/pkg/module"
	"github.com/prometheus/client_golang/prometheus"
)

// Collector exposes leaked worker slots by queue through the existing metrics sink.
func (m *Module) Collector() prometheus.Collector { return claimCollector{m: m} }

var _ module.PrometheusCollectorModule = (*Module)(nil)
