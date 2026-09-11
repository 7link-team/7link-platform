package provisioning

import (
	"context"
	"time"

	"7link.com/platform/internal/iam/provisioning/query"
	"7link.com/platform/pkg/module"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	syncPageSize       = 50
	syncTargetPageSize = 8
	syncBatchTimeout   = 30 * time.Second
)

var scimMetricDescriptions = []*prometheus.Desc{
	prometheus.NewDesc("iam_scim_sync_batches_total", "SCIM reconciliation batches attempted by this process.", nil, nil),
	prometheus.NewDesc("iam_scim_sync_failed_batches_total", "SCIM reconciliation batches with failures.", nil, nil),
	prometheus.NewDesc("iam_scim_sync_last_success_timestamp_seconds", "Last successful SCIM reconciliation batch.", nil, nil),
	prometheus.NewDesc("iam_scim_unfinished_operations", "Durable outbound operations, including requests with unknown outcomes; these must never expire automatically.", nil, nil),
	prometheus.NewDesc("iam_scim_oldest_operation_age_seconds", "Age of the oldest unfinished outbound operation.", nil, nil),
	prometheus.NewDesc("iam_scim_failed_targets", "Targets with a failed reconciliation batch awaiting recovery.", nil, nil),
	prometheus.NewDesc("iam_scim_retiring_targets", "Targets whose retirement has not completed.", nil, nil),
	prometheus.NewDesc("iam_scim_sync_pending_mappings", "Live mappings on active targets due for reconciliation.", nil, nil),
	prometheus.NewDesc("iam_scim_metrics_database_success", "Whether SCIM operational state could be read for this scrape.", nil, nil),
	prometheus.NewDesc("iam_scim_operation_release_failures_total", "Failures to release a definitively completed operation.", nil, nil),
	prometheus.NewDesc("iam_scim_sync_enabled", "Whether periodic SCIM reconciliation is enabled on this process.", nil, nil),
}

// Collector exposes independent worker health without removing login servers
// from readiness when a remote directory is down.
func (m *Module) Collector() prometheus.Collector { return m }

func (m *Module) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range scimMetricDescriptions {
		ch <- desc
	}
}

func (m *Module) Collect(ch chan<- prometheus.Metric) {
	values := []float64{float64(m.syncRuns.Load()), float64(m.syncFailures.Load()), float64(m.syncLastSuccess.Load()), 0, 0, 0, 0, 0, 0, 0, 0}
	if m.config.SyncInterval > 0 {
		values[10] = 1
	}
	if m.service != nil && m.service.repo != nil && m.service.repo.writer() != nil {
		values[9] = float64(m.service.operationReleaseFailures.Load())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var state struct {
			Unfinished int64
			Failed     int64
			Retiring   int64
		}
		names := query.BindNames(m.service.repo.prefix())
		targets, targetResources := names.Targets.String(), names.TargetResources.String()
		err := m.service.repo.writer().WithContext(ctx).Table(targets).
			Select("COALESCE(SUM(CASE WHEN operation_token <> 0 THEN 1 ELSE 0 END), 0) AS unfinished, COALESCE(SUM(CASE WHEN sync_failures > 0 THEN 1 ELSE 0 END), 0) AS failed, COALESCE(SUM(CASE WHEN retiring THEN 1 ELSE 0 END), 0) AS retiring").Scan(&state).Error
		if err == nil {
			values[3], values[5], values[6] = float64(state.Unfinished), float64(state.Failed), float64(state.Retiring)
			var oldest targetRow
			err = m.service.repo.writer().WithContext(ctx).Table(targets).Select("operation_started_at").Where("operation_token <> 0").Order("operation_started_at ASC").Limit(1).Find(&oldest).Error
			oldestRead := err == nil
			if oldest.OperationStartedAt != nil {
				values[4] = max(0, m.service.now().Sub(*oldest.OperationStartedAt).Seconds())
			}
			var pending int64
			err = m.service.repo.writer().WithContext(ctx).Table(targetResources+" AS r").
				Joins("JOIN "+targets+" AS t ON t.id = r.target_id").
				Where("r.deleted_at IS NULL AND t.status = ? AND t.retiring = ? AND t.sync_after_ms <= ?", TargetActive, false, m.service.now().UnixMilli()).
				Where("r.resource_type > t.sync_resource_type OR (r.resource_type = t.sync_resource_type AND r.resource_id > t.sync_resource_id)").Count(&pending).Error
			if err == nil && oldestRead {
				values[7], values[8] = float64(pending), 1
			}
		}
	}
	for i, desc := range scimMetricDescriptions {
		kind := prometheus.GaugeValue
		if i == 0 || i == 1 || i == 9 {
			kind = prometheus.CounterValue
		}
		ch <- prometheus.MustNewConstMetric(desc, kind, values[i])
	}
}

var _ module.PrometheusCollectorModule = (*Module)(nil)
