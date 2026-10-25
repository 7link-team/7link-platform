package app

import (
	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/execution"
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
	"7link.com/platform/internal/iam/webhook"
	audit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/configinspector"
	"7link.com/platform/internal/infra/databasemonitor"
	"7link.com/platform/internal/infra/deployment"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	metrics "7link.com/platform/internal/infra/metrics"
	"7link.com/platform/internal/infra/migrationstatus"
	"7link.com/platform/internal/infra/pprof"
	"7link.com/platform/internal/infra/secrets"
	"7link.com/platform/internal/infra/telemetry"
	"7link.com/platform/internal/knowledge"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
)

func fileConfig(c AppConfig) files.Config {
	roots := c.Files.LegacyLocalRoots
	if roots == nil {
		roots = map[string]string{"avatars": "data/avatars"}
	}
	backends := make(map[string]files.BackendConfig, len(c.Files.ReadBackends))
	for name, backend := range c.Files.ReadBackends {
		backends[name] = files.BackendConfig{Driver: backend.Driver, Options: backend.Options}
	}
	return files.Config{Config: iamActivationConfig(c), LocalRoot: c.Files.LocalRoot, LegacyLocalRoots: roots, Backend: c.Files.Backend, Options: files.DriverConfig(c.Files.Options), ReadBackends: backends}
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

func deploymentConfig(c AppConfig) deployment.Config {
	return deployment.Config{Name: c.Deployment.Name, Organization: c.Deployment.Organization}
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
	return oauth.Config{
		Config: iamActivationConfig(c),
		Consent: oauth.ConsentConfig{
			Path: c.OAuth.Consent.Path, TemplateFile: c.OAuth.Consent.TemplateFile, TTL: c.OAuth.Consent.TTL,
		},
		Device: oauth.DeviceConfig{
			VerificationURI: c.OAuth.Device.VerificationURI,
			CodeTTL:         c.OAuth.Device.CodeTTL, PollInterval: c.OAuth.Device.PollInterval,
		},
	}
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

func iamWebhookConfig(c AppConfig) webhook.Config {
	return webhook.Config{
		Config: module.Config{Enabled: c.Webhook.Enabled}, EncryptionKey: c.Webhook.EncryptionKey,
		EncryptionKeyFile:          c.Webhook.EncryptionKeyFile,
		PreviousEncryptionKeys:     c.Webhook.PreviousEncryptionKeys,
		PreviousEncryptionKeyFiles: c.Webhook.PreviousEncryptionKeyFiles,
		OutboundAllowPlaintext:     c.Webhook.OutboundAllowPlaintext,
		OutboundAllowNetworks:      c.Webhook.OutboundAllowNetworks,
		HTTPTimeout:                c.Webhook.HTTPTimeout, PollInterval: c.Webhook.PollInterval,
		RetryBase: c.Webhook.RetryBase, MaxAttempts: c.Webhook.MaxAttempts,
		DeliveryBatch: c.Webhook.DeliveryBatch, FanOutBatch: c.Webhook.FanOutBatch,
		FanOutSubscriptions: c.Webhook.FanOutSubscriptions,
	}
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

func jobsConfig(c AppConfig) jobs.Config {
	return jobs.Config{
		WorkersEnabled: &c.Jobs.WorkersEnabled, SchedulerEnabled: &c.Jobs.SchedulerEnabled,
		Queues: c.Jobs.Queues, PollInterval: c.Jobs.PollInterval, ClaimBatch: c.Jobs.ClaimBatch,
		Lease: c.Jobs.Lease, MaxRunningPerTenant: c.Jobs.MaxRunningPerTenant,
		MaxPendingPerTenant: c.Jobs.MaxPendingPerTenant, DrainTimeout: c.Jobs.DrainTimeout,
	}
}

func clientworkConfig(c AppConfig) clientwork.Config {
	return clientwork.Config{Config: iamActivationConfig(c), SubmissionBundles: c.SubmissionBundles, HiddenVerifier: clientwork.HiddenVerifierConfig{RuntimeAuthEnv: c.HiddenVerifier.RuntimeAuthEnv, OSUser: c.HiddenVerifier.OSUser, OSDomain: c.HiddenVerifier.OSDomain, PasswordEnv: c.HiddenVerifier.PasswordEnv, PrincipalID: guid.ID(c.HiddenVerifier.PrincipalID), Executable: c.HiddenVerifier.Executable, Args: append([]string(nil), c.HiddenVerifier.Args...), Model: c.HiddenVerifier.Model, Family: c.HiddenVerifier.Family, TimeoutSec: c.HiddenVerifier.TimeoutSec}}
}

func knowledgeConfig(c AppConfig) knowledge.Config {
	return knowledge.Config{Config: iamActivationConfig(c), Embedding: knowledge.EmbeddingDeploymentConfig{Enabled: c.Knowledge.Embedding.Enabled, AllowedHosts: append([]string(nil), c.Knowledge.Embedding.AllowedHosts...), AllowedNetworks: append([]string(nil), c.Knowledge.Embedding.AllowedNetworks...)}, Remote: knowledge.RemoteConfig{
		Enabled: c.Knowledge.Remote.Enabled, RepositoryHost: c.Knowledge.Remote.RepositoryHost,
		RepositoryAPIBaseURL: c.Knowledge.Remote.RepositoryAPIBaseURL, RepositoryToken: c.Knowledge.Remote.RepositoryToken,
	}}
}

func collaborationConfig(c AppConfig) collab.Config {
	return collab.Config{Config: iamActivationConfig(c), ConnectorEncryptionKey: c.Collaboration.ConnectorEncryptionKey, ConnectorEncryptionKeyFile: c.Collaboration.ConnectorEncryptionKeyFile, PreviousConnectorEncryptionKeys: append([]string(nil), c.Collaboration.PreviousConnectorEncryptionKeys...), PreviousConnectorEncryptionKeyFiles: append([]string(nil), c.Collaboration.PreviousConnectorEncryptionKeyFiles...)}
}

func businessSecretsConfig(c AppConfig) secrets.Config {
	return secrets.Config{Config: module.Config{Enabled: c.Secrets.Enabled}, ActiveKey: c.Secrets.ActiveKey, ActiveKeyFile: c.Secrets.ActiveKeyFile, PreviousKeys: append([]string(nil), c.Secrets.PreviousKeys...), PreviousKeyFiles: append([]string(nil), c.Secrets.PreviousKeyFiles...)}
}

func executionModuleConfig(config AppConfig) execution.Config {
	result := config.Execution
	result.Config = module.Config{Enabled: config.Authn.Enabled}
	return result
}
