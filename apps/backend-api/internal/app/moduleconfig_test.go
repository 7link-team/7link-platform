package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTelemetryConfigMapsOTLPSettings(t *testing.T) {
	config := telemetryConfig(AppConfig{
		Telemetry: Telemetry{OTLP: TelemetryOTLP{
			Enabled:  true,
			Protocol: "http/protobuf",
			Endpoint: "collector:4318",
			Insecure: true,
			Headers:  map[string]string{"authorization": "token"},
			Timeout:  5 * time.Second,
		}},
	})
	assert.True(t, config.OTLP.Enabled)
	assert.Equal(t, "http/protobuf", config.OTLP.Protocol)
	assert.Equal(t, "collector:4318", config.OTLP.Endpoint)
	assert.True(t, config.OTLP.Insecure)
	assert.Equal(t, "token", config.OTLP.Headers["authorization"])
	assert.Equal(t, 5*time.Second, config.OTLP.Timeout)
}

func TestAuditConfigMapsSettings(t *testing.T) {
	config := auditConfig(AppConfig{
		Audit: Audit{
			Enabled:         true,
			RecordReads:     true,
			RecordStreams:   true,
			CaptureBodies:   true,
			MaxBodyBytes:    4096,
			SensitiveFields: []string{"secret"},
			SkipRoutes:      []string{"/healthz"},
			QueueSize:       2048,
			BatchSize:       100,
			FlushInterval:   500 * time.Millisecond,
			FlushTimeout:    3 * time.Second,
			Overflow:        "block",
		},
	})
	assert.True(t, config.Enabled)
	assert.True(t, config.RecordReads)
	assert.True(t, config.RecordStreams)
	assert.True(t, config.CaptureBodies)
	assert.Equal(t, 4096, config.MaxBodyBytes)
	assert.Equal(t, []string{"secret"}, config.SensitiveFields)
	assert.Equal(t, []string{"/healthz"}, config.SkipRoutes)
	assert.Equal(t, 2048, config.QueueSize)
	assert.Equal(t, 100, config.BatchSize)
	assert.Equal(t, 500*time.Millisecond, config.FlushInterval)
	assert.Equal(t, 3*time.Second, config.FlushTimeout)
	assert.Equal(t, "block", config.Overflow)
}

func TestAuditConfigMapsDatabaseGate(t *testing.T) {
	config := auditConfig(AppConfig{Audit: Audit{Database: AuditDatabase{Enabled: true}}})
	assert.True(t, config.Database.Enabled)
}

func TestDatasourceMonitorConfigMapsSettings(t *testing.T) {
	config := datasourceMonitorConfig(AppConfig{
		API: API{DatasourceInspector: APIDatasourceInspector{Enabled: true}},
		DatasourceMonitor: DatasourceMonitor{
			Interval:         5 * time.Second,
			Timeout:          1 * time.Second,
			StaleAfter:       15 * time.Second,
			SQLCapacity:      200,
			SlowCapacity:     50,
			SlowSQLThreshold: 100 * time.Millisecond,
		},
	})
	assert.Equal(t, 5*time.Second, config.Interval)
	assert.Equal(t, 1*time.Second, config.Timeout)
	assert.Equal(t, 15*time.Second, config.StaleAfter)
	assert.True(t, config.ExposeHTTP)
	assert.Equal(t, 200, config.SQLCapacity)
	assert.Equal(t, 50, config.SlowCapacity)
	assert.Equal(t, 100*time.Millisecond, config.SlowThreshold)
}

func TestConditionalModuleConfigsMapEnabled(t *testing.T) {
	assert.True(t, metricsConfig(AppConfig{API: API{Metrics: APIMetrics{Enabled: true}}}).Enabled)
	assert.True(t, migrationStatusConfig(AppConfig{API: API{MigrationStatus: APIMigrationStatus{Enabled: true}}}).Enabled)
	assert.True(t, configInspectorConfig(AppConfig{API: API{ConfigInspector: APIConfigInspector{Enabled: true}}}).Enabled)
	assert.True(t, pprofConfig(AppConfig{Pprof: Pprof{Enabled: true}}).Enabled)

	assert.False(t, metricsConfig(AppConfig{}).Enabled)
	assert.False(t, migrationStatusConfig(AppConfig{}).Enabled)
	assert.False(t, configInspectorConfig(AppConfig{}).Enabled)
	assert.False(t, pprofConfig(AppConfig{}).Enabled)
}
