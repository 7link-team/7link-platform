package app

import (
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestFileConfigMapsActiveAndLegacyStorage(t *testing.T) {
	c := AppConfig{}
	c.Files.Backend = "s3"
	c.Files.Options = map[string]string{"endpoint": "https://storage.example.test", "bucket": "files"}
	c.Files.ReadBackends = map[string]FileReadBackendConfig{"local": {Driver: "local", Options: map[string]string{"root": "data/old-files"}}}
	got := fileConfig(c)
	assert.Equal(t, "s3", got.Backend)
	assert.Equal(t, "https://storage.example.test", got.Options["endpoint"])
	assert.Equal(t, "local", got.ReadBackends["local"].Driver)
	assert.Equal(t, "data/old-files", got.ReadBackends["local"].Options["root"])
}

func TestClientworkSubmissionBundlesRequireExplicitDeploymentKeys(t *testing.T) {
	assert.Empty(t, clientworkConfig(AppConfig{}).SubmissionBundles.SigningKey)
	c := AppConfig{}
	c.SubmissionBundles.SigningKey = "explicit-deployment-key"
	c.SubmissionBundles.PreviousSigningKeys = []string{"previous-deployment-key"}
	got := clientworkConfig(c)
	assert.Equal(t, c.SubmissionBundles, got.SubmissionBundles)
}

func TestKnowledgeRemoteConfigMapsDeploymentSettings(t *testing.T) {
	input := AppConfig{Knowledge: KnowledgeConfig{Remote: KnowledgeRemoteConfig{Enabled: true, RepositoryHost: "git.example.net", RepositoryAPIBaseURL: "https://api.example.net", RepositoryToken: "deployment-credential"}}}
	input.Authn.Enabled = true
	config := knowledgeConfig(input)
	assert.True(t, config.Enabled)
	assert.True(t, config.Remote.Enabled)
	assert.Equal(t, input.Knowledge.Remote.RepositoryHost, config.Remote.RepositoryHost)
	assert.Equal(t, input.Knowledge.Remote.RepositoryAPIBaseURL, config.Remote.RepositoryAPIBaseURL)
	assert.Equal(t, input.Knowledge.Remote.RepositoryToken, config.Remote.RepositoryToken)
}

func TestKnowledgeConfigMapsEmbeddingIsolation(t *testing.T) {
	input := AppConfig{Knowledge: KnowledgeConfig{Embedding: KnowledgeEmbeddingConfig{Enabled: true, AllowedHosts: []string{"embed.example.net"}, AllowedNetworks: []string{"192.0.2.0/24"}}}}
	config := knowledgeConfig(input)
	assert.True(t, config.Embedding.Enabled)
	assert.Equal(t, input.Knowledge.Embedding.AllowedHosts, config.Embedding.AllowedHosts)
	assert.Equal(t, input.Knowledge.Embedding.AllowedNetworks, config.Embedding.AllowedNetworks)
	config.Embedding.AllowedHosts[0] = "changed.example.net"
	config.Embedding.AllowedNetworks[0] = "198.51.100.0/24"
	assert.Equal(t, "embed.example.net", input.Knowledge.Embedding.AllowedHosts[0])
	assert.Equal(t, "192.0.2.0/24", input.Knowledge.Embedding.AllowedNetworks[0])
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

func TestIAMModuleConfigsMapEverySetting(t *testing.T) {
	config := AppConfig{
		Authn: IAMAuthn(authnext.Config{
			Enabled: true, Issuer: "https://issuer.example", Audience: []string{"api"},
			AccessTokenTTL: 9 * time.Minute,
		}),
		IAMAudit: IAMAudit{Anchor: IAMAuditAnchor{
			Enabled: true, Endpoint: "https://objects.example", Bucket: "anchors", Region: "us-test-1",
			AccessKey: "access", SecretKey: "secret", RetentionDays: 90, SigningKey: "signing",
		}, Archive: IAMAuditArchive{Enabled: true, Interval: 2 * time.Minute, Timeout: 3 * time.Minute},
			ErasureLedger: IAMAuditErasureLedger{Enabled: true, Timeout: 4 * time.Second}},
		Provisioning: IAMProvisioning{
			EncryptionKey: "key", EncryptionKeyFile: "key-file", HTTPTimeout: 12 * time.Second,
			SyncInterval: time.Minute,
		},
		Federation: IAMFederation{
			Enabled: true, EncryptionKey: "federation-key", EncryptionKeyFile: "federation-key-file",
			HTTPTimeout: 13 * time.Second, ClockSkew: 14 * time.Second, StateTTL: 15 * time.Minute,
			SAML: IAMFederationSAML{
				Enabled: true, SigningKeyFile: "saml-key", CertificateFile: "saml-cert", LoginPath: "/sign-in",
				ValidDuration: 16 * time.Minute, MaxIssueDelay: 17 * time.Minute,
			},
		},
	}

	assert.Equal(t, authnext.Config(config.Authn), iamAuthnConfig(config).Service)
	assert.True(t, iamAuthnConfig(config).Enabled)
	assert.True(t, iamAuditConfig(config).Enabled)
	assert.Equal(t, config.IAMAudit.Anchor.Enabled, iamAuditConfig(config).Anchor.Enabled)
	assert.Equal(t, config.IAMAudit.Anchor.Endpoint, iamAuditConfig(config).Anchor.Endpoint)
	assert.Equal(t, config.IAMAudit.Anchor.Bucket, iamAuditConfig(config).Anchor.Bucket)
	assert.Equal(t, config.IAMAudit.Anchor.Region, iamAuditConfig(config).Anchor.Region)
	assert.Equal(t, config.IAMAudit.Anchor.AccessKey, iamAuditConfig(config).Anchor.AccessKey)
	assert.Equal(t, config.IAMAudit.Anchor.SecretKey, iamAuditConfig(config).Anchor.SecretKey)
	assert.Equal(t, config.IAMAudit.Anchor.RetentionDays, iamAuditConfig(config).Anchor.RetentionDays)
	assert.Equal(t, config.IAMAudit.Anchor.SigningKey, iamAuditConfig(config).Anchor.SigningKey)
	assert.Equal(t, config.IAMAudit.Archive.Enabled, iamAuditConfig(config).Archive.Enabled)
	assert.Equal(t, config.IAMAudit.Archive.Interval, iamAuditConfig(config).Archive.Interval)
	assert.Equal(t, config.IAMAudit.Archive.Timeout, iamAuditConfig(config).Archive.Timeout)
	assert.Equal(t, config.IAMAudit.ErasureLedger.Enabled, iamAuditConfig(config).ErasureLedger.Enabled)
	assert.Equal(t, config.IAMAudit.ErasureLedger.Timeout, iamAuditConfig(config).ErasureLedger.Timeout)
	assert.Equal(t, config.Provisioning.EncryptionKey, iamProvisioningConfig(config).EncryptionKey)
	assert.True(t, iamProvisioningConfig(config).Enabled)
	assert.Equal(t, config.Provisioning.EncryptionKeyFile, iamProvisioningConfig(config).EncryptionKeyFile)
	assert.Equal(t, config.Provisioning.HTTPTimeout, iamProvisioningConfig(config).HTTPTimeout)
	assert.Equal(t, config.Provisioning.SyncInterval, iamProvisioningConfig(config).SyncInterval)
	assert.Equal(t, config.Federation.Enabled, iamFederationConfig(config).Enabled)
	assert.Equal(t, config.Federation.EncryptionKey, iamFederationConfig(config).EncryptionKey)
	assert.Equal(t, config.Federation.EncryptionKeyFile, iamFederationConfig(config).EncryptionKeyFile)
	assert.Equal(t, config.Federation.HTTPTimeout, iamFederationConfig(config).HTTPTimeout)
	assert.Equal(t, config.Federation.ClockSkew, iamFederationConfig(config).ClockSkew)
	assert.Equal(t, config.Federation.StateTTL, iamFederationConfig(config).StateTTL)
	assert.Equal(t, config.Federation.SAML.Enabled, iamFederationConfig(config).SAML.Enabled)
	assert.Equal(t, config.Federation.SAML.SigningKeyFile, iamFederationConfig(config).SAML.SigningKeyFile)
	assert.Equal(t, config.Federation.SAML.CertificateFile, iamFederationConfig(config).SAML.CertificateFile)
	assert.Equal(t, config.Federation.SAML.LoginPath, iamFederationConfig(config).SAML.LoginPath)
	assert.Equal(t, config.Federation.SAML.ValidDuration, iamFederationConfig(config).SAML.ValidDuration)
	assert.Equal(t, config.Federation.SAML.MaxIssueDelay, iamFederationConfig(config).SAML.MaxIssueDelay)
	assert.True(t, iamCoreConfig(config).Enabled)
	assert.True(t, iamIdentityConfig(config).Enabled)
	assert.True(t, iamOrganizationConfig(config).Enabled)
	assert.False(t, iamOrganizationConfig(AppConfig{}).TenantRegistrationEnabled)
	registrationConfig := config
	registrationConfig.IAM.TenantRegistrationEnabled = true
	assert.True(t, iamOrganizationConfig(registrationConfig).TenantRegistrationEnabled)
	assert.True(t, iamOAuthConfig(config).Enabled)
	assert.True(t, iamGovernanceConfig(config).Enabled)
}

func TestClientworkConfigMapsPrivateVerifierWithoutAliasingArguments(t *testing.T) {
	c := AppConfig{Authn: IAMAuthn{Enabled: true}, HiddenVerifier: HiddenVerifierConfig{OSUser: "private-verifier", OSDomain: ".", PasswordEnv: "PRIVATE_VERIFIER_PASSWORD", RuntimeAuthEnv: "PRIVATE_VERIFIER_MODEL_KEY", PrincipalID: 91, Executable: "/trusted/runner", Args: []string{"hidden-verifier", "--runtime", "CLAUDE_CODE"}, Model: "available-model", Family: "anthropic", TimeoutSec: 70}}
	got := clientworkConfig(c)
	assert.True(t, got.Enabled)
	assert.EqualValues(t, 91, got.HiddenVerifier.PrincipalID)
	assert.Equal(t, c.HiddenVerifier.OSUser, got.HiddenVerifier.OSUser)
	assert.Equal(t, c.HiddenVerifier.OSDomain, got.HiddenVerifier.OSDomain)
	assert.Equal(t, c.HiddenVerifier.PasswordEnv, got.HiddenVerifier.PasswordEnv)
	assert.Equal(t, c.HiddenVerifier.RuntimeAuthEnv, got.HiddenVerifier.RuntimeAuthEnv)
	assert.Equal(t, c.HiddenVerifier.Executable, got.HiddenVerifier.Executable)
	assert.Equal(t, c.HiddenVerifier.Model, got.HiddenVerifier.Model)
	assert.Equal(t, c.HiddenVerifier.Family, got.HiddenVerifier.Family)
	assert.Equal(t, c.HiddenVerifier.TimeoutSec, got.HiddenVerifier.TimeoutSec)
	assert.Equal(t, c.HiddenVerifier.Args, got.HiddenVerifier.Args)
	c.HiddenVerifier.Args[0] = "mutated"
	assert.Equal(t, "hidden-verifier", got.HiddenVerifier.Args[0])
}

func TestBusinessSecretsConfigurationPreservesActivationAndKeyReferences(t *testing.T) {
	c := AppConfig{}
	assert.False(t, businessSecretsConfig(c).Enabled)
	c.Secrets = BusinessSecretsConfig{Enabled: true, ActiveKeyFile: "active.key", PreviousKeyFiles: []string{"old.key"}}
	mapped := businessSecretsConfig(c)
	assert.True(t, mapped.Enabled)
	assert.Equal(t, "active.key", mapped.ActiveKeyFile)
	assert.Equal(t, []string{"old.key"}, mapped.PreviousKeyFiles)
	mapped.PreviousKeyFiles[0] = "changed"
	assert.Equal(t, "old.key", c.Secrets.PreviousKeyFiles[0])
}

func TestExecutionModuleConfigPreservesRetentionAndAuthnEnablement(t *testing.T) {
	config := validTestAppConfig()
	config.Authn.Enabled = true
	config.Execution.Config.Enabled = false
	config.Execution.Terminal.RecordingRetentionDays = 30
	got := executionModuleConfig(config)
	require.True(t, got.Enabled)
	require.Equal(t, 30, got.Terminal.RecordingRetentionDays)
	require.False(t, config.Execution.Enabled, "composition must not mutate deployment configuration")
	config.Authn.Enabled = false
	config.Execution.Config.Enabled = true
	got = executionModuleConfig(config)
	require.False(t, got.Enabled)
	require.Equal(t, 30, got.Terminal.RecordingRetentionDays)
}
