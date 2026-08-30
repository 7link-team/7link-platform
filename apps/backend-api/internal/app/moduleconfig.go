package app

import (
	audit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/configinspector"
	"7link.com/platform/internal/infra/databasemonitor"
	metrics "7link.com/platform/internal/infra/metrics"
	"7link.com/platform/internal/infra/migrationstatus"
	"7link.com/platform/internal/infra/pprof"
	"7link.com/platform/internal/infra/telemetry"
	"7link.com/platform/pkg/module"
)

// moduleconfig.go is the only seam between AppConfig and per-module configs.
// AppConfig never imports module packages (modules cannot import internal/app —
// import cycle); these functions map one into the other. Every mapping field is
// locked by moduleconfig_test.go.

func telemetryConfig(c AppConfig) telemetry.Config {
	return telemetry.Config{
		OTLP: telemetry.OTLPConfig{
			Enabled:  c.Telemetry.OTLP.Enabled,
			Protocol: c.Telemetry.OTLP.Protocol,
			Endpoint: c.Telemetry.OTLP.Endpoint,
			Insecure: c.Telemetry.OTLP.Insecure,
			Headers:  c.Telemetry.OTLP.Headers,
			Timeout:  c.Telemetry.OTLP.Timeout,
		},
	}
}

func auditConfig(c AppConfig) audit.Config {
	return audit.Config{
		Enabled:         c.Audit.Enabled,
		Database:        audit.DatabaseConfig{Enabled: c.Audit.Database.Enabled},
		RecordReads:     c.Audit.RecordReads,
		RecordStreams:   c.Audit.RecordStreams,
		MaxBodyBytes:    c.Audit.MaxBodyBytes,
		SensitiveFields: c.Audit.SensitiveFields,
		SkipRoutes:      c.Audit.SkipRoutes,
		QueueSize:       c.Audit.QueueSize,
		BatchSize:       c.Audit.BatchSize,
		FlushInterval:   c.Audit.FlushInterval,
		FlushTimeout:    c.Audit.FlushTimeout,
		Overflow:        c.Audit.Overflow,
	}
}

func datasourceMonitorConfig(c AppConfig) databasemonitor.Config {
	return databasemonitor.Config{
		Interval:      c.DatasourceMonitor.Interval,
		Timeout:       c.DatasourceMonitor.Timeout,
		StaleAfter:    c.DatasourceMonitor.StaleAfter,
		ExposeHTTP:    c.API.DatasourceInspector.Enabled,
		SQLCapacity:   c.DatasourceMonitor.SQLCapacity,
		SlowCapacity:  c.DatasourceMonitor.SlowCapacity,
		SlowThreshold: c.DatasourceMonitor.SlowSQLThreshold,
	}
}

func metricsConfig(c AppConfig) metrics.Config {
	return metrics.Config{Config: module.Config{Enabled: c.API.Metrics.Enabled}}
}

func migrationStatusConfig(c AppConfig) migrationstatus.Config {
	return migrationstatus.Config{Config: module.Config{Enabled: c.API.MigrationStatus.Enabled}}
}

func configInspectorConfig(c AppConfig) configinspector.Config {
	return configinspector.Config{Config: module.Config{Enabled: c.API.ConfigInspector.Enabled}}
}

func pprofConfig(c AppConfig) pprof.Config {
	return pprof.Config{Config: module.Config{Enabled: c.Pprof.Enabled}}
}
