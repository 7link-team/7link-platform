package realtime

import "github.com/prometheus/client_golang/prometheus"

func (h *Hub) collectors() []prometheus.Collector {
	return []prometheus.Collector{h.dropped, h.streams, h.frames, h.latency, h.resync, h.denied}
}
func (h *Hub) Describe(ch chan<- *prometheus.Desc) {
	for _, collector := range h.collectors() {
		collector.Describe(ch)
	}
}
func (h *Hub) Collect(ch chan<- prometheus.Metric) {
	for _, collector := range h.collectors() {
		collector.Collect(ch)
	}
}
