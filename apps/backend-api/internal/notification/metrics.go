package notification

import (
	"7link.com/platform/pkg/module"
	"github.com/prometheus/client_golang/prometheus"
)

// Collector exposes actual completed SMTP attempts in this process. A success
// resets the consecutive failure count; suppressed and deferred jobs never send.
func (m *Module) Collector() prometheus.Collector {
	return prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "notification_smtp_consecutive_failures",
		Help: "Consecutive failed SMTP attempts since the last success or process restart.",
	}, func() float64 { return float64(m.smtpFailures.Load()) })
}

var _ module.PrometheusCollectorModule = (*Module)(nil)
