package audit

import (
	"context"
	"errors"
	dto "github.com/prometheus/client_model/go"
	"net/http"
	"sync/atomic"

	"github.com/minio/minio-go/v7"
	"github.com/prometheus/client_golang/prometheus"
)

// s3Telemetry observes the actual audit S3 transport only. It deliberately
// exports no files-backend claim: that module currently has no S3 driver.
type s3Telemetry struct {
	observed  atomic.Bool
	next      http.RoundTripper
	available *prometheus.GaugeVec
	requests  *prometheus.CounterVec
}

func newS3Telemetry(secure bool) (*s3Telemetry, error) {
	next, err := minio.DefaultTransport(secure)
	if err != nil {
		return nil, err
	}
	return &s3Telemetry{next: next,
		available: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "iam_audit_s3_available", Help: "Whether the last actual audit S3 HTTP attempt could access the service. No sample until an attempt; this does not describe file storage."}, nil),
		requests:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "iam_audit_s3_requests_total", Help: "Actual audit S3 HTTP attempts by bounded operation and outcome, including SDK retries."}, []string{"operation", "outcome"}),
	}, nil
}
func (t *s3Telemetry) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	// Caller cancellation and shutdown do not prove storage is unavailable.
	if errors.Is(req.Context().Err(), context.Canceled) {
		return resp, err
	}
	healthy := err == nil && resp != nil && resp.StatusCode < 500 && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests
	outcome, value := "success", float64(1)
	if !healthy {
		outcome, value = "failed", 0
	}
	operation := req.Method
	switch operation {
	case "GET", "HEAD", "PUT", "DELETE", "POST":
	default:
		operation = "OTHER"
	}
	t.available.WithLabelValues().Set(value)
	t.requests.WithLabelValues(operation, outcome).Inc()
	t.observed.Store(true)
	return resp, err
}

var s3AvailabilityDesc = prometheus.NewDesc("iam_audit_s3_available", "Whether every observed audit S3 store could access the service on its last actual attempt. No sample until an attempt; this does not describe file storage.", nil, nil)
var s3RequestsDesc = prometheus.NewDesc("iam_audit_s3_requests_total", "Actual audit S3 HTTP attempts by bounded operation and outcome, including SDK retries.", []string{"operation", "outcome"}, nil)

func (m *Module) auditS3Telemetry() []*s3Telemetry {
	if m.service == nil {
		return nil
	}
	stores := []*AnchorStore{m.service.anchor, m.service.archive}
	if m.service.erasureLedger != nil {
		stores = append(stores, m.service.erasureLedger.store)
	}
	seen := map[*s3Telemetry]bool{}
	out := []*s3Telemetry{}
	for _, store := range stores {
		if store != nil && store.telemetry != nil && !seen[store.telemetry] {
			seen[store.telemetry] = true
			out = append(out, store.telemetry)
		}
	}
	return out
}
func (m *Module) describeS3Telemetry(ch chan<- *prometheus.Desc) {
	ch <- s3AvailabilityDesc
	ch <- s3RequestsDesc
}
func (m *Module) collectS3Telemetry(ch chan<- prometheus.Metric) {
	observed := false
	available := float64(1)
	counts := map[[2]string]float64{}
	for _, t := range m.auditS3Telemetry() {
		if !t.observed.Load() {
			continue
		}
		observed = true
		sample := &dto.Metric{}
		if t.available.WithLabelValues().Write(sample) != nil {
			continue
		}
		available = min(available, sample.GetGauge().GetValue())
		for _, operation := range []string{"GET", "HEAD", "PUT", "DELETE", "POST", "OTHER"} {
			for _, outcome := range []string{"success", "failed"} {
				sample := &dto.Metric{}
				if t.requests.WithLabelValues(operation, outcome).Write(sample) == nil {
					counts[[2]string{operation, outcome}] += sample.GetCounter().GetValue()
				}
			}
		}
	}
	if !observed {
		return
	}
	ch <- prometheus.MustNewConstMetric(s3AvailabilityDesc, prometheus.GaugeValue, available)
	for labels, value := range counts {
		ch <- prometheus.MustNewConstMetric(s3RequestsDesc, prometheus.CounterValue, value, labels[0], labels[1])
	}
}
