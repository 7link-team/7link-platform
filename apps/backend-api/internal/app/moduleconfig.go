package app

import (
	"7link.com/platform/internal/files"
	iamaudit "7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	audit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/configinspector"
	"7link.com/platform/internal/infra/databasemonitor"
	metrics "7link.com/platform/internal/infra/metrics"
	"7link.com/platform/internal/infra/migrationstatus"
	"7link.com/platform/internal/infra/pprof"
	"7link.com/platform/internal/infra/telemetry"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
)

func fileConfig(c AppConfig) files.Config {
	roots := c.Files.LegacyLocalRoots
	if roots == nil {
		roots = map[string]string{"avatars": "data/avatars"}
	}
	return files.Config{Config: iamActivationConfig(c), LocalRoot: c.Files.LocalRoot, LegacyLocalRoots: roots}
}

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
		CaptureBodies:   c.Audit.CaptureBodies,
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

func iamAuthnConfig(c AppConfig) iamauthn.Config {
	return iamauthn.Config{
		Config:  iamActivationConfig(c),
		Service: authnext.Config(c.Authn),
	}
}

func iamAuditConfig(c AppConfig) iamaudit.Config {
	return iamaudit.Config{Config: iamActivationConfig(c), Anchor: iamaudit.AnchorConfig{
		Enabled: c.IAMAudit.Anchor.Enabled, Endpoint: c.IAMAudit.Anchor.Endpoint,
		Bucket: c.IAMAudit.Anchor.Bucket, Region: c.IAMAudit.Anchor.Region,
		AccessKey: c.IAMAudit.Anchor.AccessKey, SecretKey: c.IAMAudit.Anchor.SecretKey,
		RetentionDays: c.IAMAudit.Anchor.RetentionDays, SigningKey: c.IAMAudit.Anchor.SigningKey,
	}, Archive: iamaudit.ArchiveConfig{
		Enabled: c.IAMAudit.Archive.Enabled, Interval: c.IAMAudit.Archive.Interval, Timeout: c.IAMAudit.Archive.Timeout,
	}, ErasureLedger: iamaudit.ErasureLedgerConfig{
		Enabled: c.IAMAudit.ErasureLedger.Enabled, Timeout: c.IAMAudit.ErasureLedger.Timeout,
	}}
}

func iamCoreConfig(c AppConfig) core.Config {
	return core.Config{
		Config:                       iamActivationConfig(c),
		RequirePlatformAdministrator: c.IAM.RequirePlatformAdministrator,
	}
}

func iamIdentityConfig(c AppConfig) identity.Config {
	return identity.Config{Config: iamActivationConfig(c)}
}

func iamOrganizationConfig(c AppConfig) organization.Config {
	return organization.Config{Config: iamActivationConfig(c), TenantRegistrationEnabled: c.IAM.TenantRegistrationEnabled}
}

func iamOAuthConfig(c AppConfig) oauth.Config {
	return oauth.Config{Config: iamActivationConfig(c), Consent: oauth.ConsentConfig{
		Path: c.OAuth.Consent.Path, TemplateFile: c.OAuth.Consent.TemplateFile, TTL: c.OAuth.Consent.TTL,
	}}
}

func iamProvisioningConfig(c AppConfig) provisioning.Config {
	return provisioning.Config{
		Config:        iamActivationConfig(c),
		EncryptionKey: c.Provisioning.EncryptionKey, EncryptionKeyFile: c.Provisioning.EncryptionKeyFile,
		HTTPTimeout: c.Provisioning.HTTPTimeout, SyncInterval: c.Provisioning.SyncInterval,
		OutboundAllowPlaintext: c.Provisioning.OutboundAllowPlaintext,
		OutboundAllowNetworks:  c.Provisioning.OutboundAllowNetworks,
	}
}

func iamGovernanceConfig(c AppConfig) governance.Config {
	return governance.Config{Config: iamActivationConfig(c)}
}

func iamFederationConfig(c AppConfig) federation.Config {
	return federation.Config{
		Config: module.Config{Enabled: c.Federation.Enabled}, EncryptionKey: c.Federation.EncryptionKey,
		EncryptionKeyFile: c.Federation.EncryptionKeyFile, HTTPTimeout: c.Federation.HTTPTimeout,
		ClockSkew: c.Federation.ClockSkew, StateTTL: c.Federation.StateTTL,
		OutboundAllowPlaintext: c.Federation.OutboundAllowPlaintext,
		OutboundAllowNetworks:  c.Federation.OutboundAllowNetworks,
		SAML: federation.SAMLConfig{
			Enabled: c.Federation.SAML.Enabled, SigningKeyFile: c.Federation.SAML.SigningKeyFile,
			CertificateFile: c.Federation.SAML.CertificateFile, LoginPath: c.Federation.SAML.LoginPath,
			ValidDuration: c.Federation.SAML.ValidDuration, MaxIssueDelay: c.Federation.SAML.MaxIssueDelay,
		},
	}
}

func iamActivationConfig(c AppConfig) module.Config {
	return module.Config{Enabled: c.Authn.Enabled}
}
