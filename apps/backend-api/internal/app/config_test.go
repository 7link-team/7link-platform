package app

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	configx "7link.com/platform/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatabaseRolesDefaultToReadWriteDuringParsing(t *testing.T) {
	var parsed AppConfig
	require.NoError(t, configx.New().Parse(&parsed, "--database.main.type=sqlite", "--database.main.dsn=:memory:"))
	database := parsed.Database["main"]
	assert.True(t, database.Writable)
	assert.True(t, database.Readable)
}

func TestPprofDefaultsToDisabledAndCanBeEnabled(t *testing.T) {
	var disabled AppConfig
	require.NoError(t, configx.New().Parse(&disabled))
	assert.False(t, disabled.Pprof.Enabled)

	var enabled AppConfig
	require.NoError(t, configx.New().Parse(&enabled, "--pprof.enabled"))
	assert.True(t, enabled.Pprof.Enabled)
}

func TestShutdownDefaultsAndOverrides(t *testing.T) {
	var defaults AppConfig
	require.NoError(t, configx.New().Parse(&defaults))
	assert.Equal(t, ShutdownConfig{
		Graceful:   true,
		Timeout:    30 * time.Second,
		DrainDelay: 5 * time.Second,
	}, defaults.Shutdown)

	var configured AppConfig
	require.NoError(t, configx.New().Parse(&configured,
		"--shutdown.graceful=false",
		"--shutdown.timeout=3s",
		"--shutdown.drain-delay=250ms",
	))
	assert.Equal(t, ShutdownConfig{
		Graceful:   false,
		Timeout:    3 * time.Second,
		DrainDelay: 250 * time.Millisecond,
	}, configured.Shutdown)
}

func TestBannerDefaultsAndOverrides(t *testing.T) {
	var defaults AppConfig
	require.NoError(t, configx.New().Parse(&defaults))
	assert.Empty(t, defaults.Name)
	assert.Equal(t, BannerConfig{Enabled: true, Type: "standard", Theme: "rainbow"}, defaults.Banner)

	var configured AppConfig
	require.NoError(t, configx.New().Parse(&configured,
		"--name=test-api",
		"--banner.enabled=false",
		"--banner.text=SEVEN LINK",
		"--banner.type=slant",
		"--banner.theme=ocean",
	))
	assert.Equal(t, "test-api", configured.Name)
	assert.Equal(t, BannerConfig{
		Enabled: false,
		Text:    "SEVEN LINK",
		Type:    "slant",
		Theme:   "ocean",
	}, configured.Banner)
}

func TestHTTPAPIResourceDefaultsAndOverrides(t *testing.T) {
	var defaults AppConfig
	require.NoError(t, configx.New().Parse(&defaults))
	assert.True(t, defaults.API.Landing.Enabled)
	assert.False(t, defaults.API.Landing.AdvertiseHealth)
	assert.Equal(t, APIDocs{Enabled: true, Advertise: true}, defaults.API.Docs)
	assert.Equal(t, APIOpenAPI{Enabled: true, Advertise: true}, defaults.API.OpenAPI)
	assert.Equal(t, APIMetrics{}, defaults.API.Metrics)
	assert.Equal(t, APIMigrationStatus{}, defaults.API.MigrationStatus)
	assert.Equal(t, APIConfigInspector{}, defaults.API.ConfigInspector)
	assert.Equal(t, APIDatasourceInspector{}, defaults.API.DatasourceInspector)
	assert.Equal(t, DatasourceMonitor{
		Interval: 10 * time.Second, Timeout: 2 * time.Second, StaleAfter: 30 * time.Second,
		SQLCapacity: 500, SlowCapacity: 200, SlowSQLThreshold: 200 * time.Millisecond,
	}, defaults.DatasourceMonitor)

	var configured AppConfig
	require.NoError(t, configx.New().Parse(&configured,
		"--api.landing.enabled=false",
		"--api.landing.advertise-health=true",
		"--api.docs.advertise=false",
		"--api.docs.enabled=false",
	))
	assert.False(t, configured.API.Landing.Enabled)
	assert.True(t, configured.API.Landing.AdvertiseHealth)
	assert.Equal(t, APIDocs{Enabled: false, Advertise: false}, configured.API.Docs)
}

func TestOTLPDefaultsAndOverrides(t *testing.T) {
	var defaults AppConfig
	require.NoError(t, configx.New().Parse(&defaults))
	assert.Equal(t, TelemetryOTLP{
		Protocol: "grpc",
		Headers:  map[string]string{},
		Timeout:  10 * time.Second,
	}, defaults.Telemetry.OTLP)

	var configured AppConfig
	require.NoError(t, configx.New().Parse(&configured,
		"--telemetry.otlp.enabled",
		"--telemetry.otlp.protocol=http/protobuf",
		"--telemetry.otlp.endpoint=collector:4318",
		"--telemetry.otlp.insecure",
		"--telemetry.otlp.headers.authorization=secret",
		"--telemetry.otlp.timeout=3s",
	))
	assert.Equal(t, TelemetryOTLP{
		Enabled:  true,
		Protocol: "http/protobuf",
		Endpoint: "collector:4318",
		Insecure: true,
		Headers:  map[string]string{"authorization": "secret"},
		Timeout:  3 * time.Second,
	}, configured.Telemetry.OTLP)
}

func TestServeMigrationDefaultsToDisabledAndCanBeEnabled(t *testing.T) {
	var disabled AppConfig
	require.NoError(t, configx.New().Parse(&disabled))
	assert.False(t, disabled.Migrate)

	var enabled AppConfig
	require.NoError(t, configx.New().Parse(&enabled, "--migrate"))
	assert.True(t, enabled.Migrate)
}

func TestServerAddressesRequireIPv4(t *testing.T) {
	valid := AppConfig{
		HttpServer: HttpServer{Address: "0.0.0.0"},
		GrpcServer: GrpcServer{Address: "0.0.0.0"},
	}
	require.NoError(t, configx.Process(&valid))

	invalidHTTP := valid
	invalidHTTP.HttpServer.Address = "localhost"
	assert.EqualError(t, configx.Process(&invalidHTTP), "config: http_server.address does not match validation pattern")

	invalidGRPC := valid
	invalidGRPC.GrpcServer.Address = "::"
	assert.EqualError(t, configx.Process(&invalidGRPC), "config: grpc_server.address does not match validation pattern")
}

func TestApplyRuntimeDefaults(t *testing.T) {
	config := AppConfig{}
	applyRuntimeDefaults(&config)
	assert.Equal(t, defaultHTTPReadHeaderTimeout, config.HttpServer.ReadHeaderTimeout)
	assert.Equal(t, defaultHTTPIdleTimeout, config.HttpServer.IdleTimeout)
	assert.Equal(t, defaultHTTPMaxHeaderBytes, config.HttpServer.MaxHeaderBytes)
	assert.Equal(t, defaultShutdownTimeout, config.Shutdown.Timeout)
	assert.Equal(t, defaultDrainDelay, config.Shutdown.DrainDelay)
	assert.Equal(t, defaultDatasourceProbeInterval, config.DatasourceMonitor.Interval)
	assert.Equal(t, defaultDatasourceProbeTimeout, config.DatasourceMonitor.Timeout)
	assert.Equal(t, defaultDatasourceStaleAfter, config.DatasourceMonitor.StaleAfter)
	assert.Equal(t, defaultDatasourceSQLCapacity, config.DatasourceMonitor.SQLCapacity)
	assert.Equal(t, defaultDatasourceSlowCapacity, config.DatasourceMonitor.SlowCapacity)
	assert.Equal(t, defaultSlowSQLThreshold, config.DatasourceMonitor.SlowSQLThreshold)

	explicit := AppConfig{
		Shutdown: ShutdownConfig{
			Graceful:   false,
			Timeout:    20 * time.Second,
			DrainDelay: time.Second,
		},
		HttpServer: HttpServer{
			ReadHeaderTimeout: time.Second,
			IdleTimeout:       time.Minute,
			MaxHeaderBytes:    4096,
		},
	}
	applyRuntimeDefaults(&explicit)
	assert.False(t, explicit.Shutdown.Graceful)
	assert.Equal(t, 20*time.Second, explicit.Shutdown.Timeout)
	assert.Equal(t, time.Second, explicit.Shutdown.DrainDelay)
	assert.Equal(t, time.Second, explicit.HttpServer.ReadHeaderTimeout)
	assert.Equal(t, time.Minute, explicit.HttpServer.IdleTimeout)
	assert.Equal(t, 4096, explicit.HttpServer.MaxHeaderBytes)
}

func TestValidateRuntimeConfigRejectsNegativeValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AppConfig)
		want   string
	}{
		{name: "shutdown", mutate: func(c *AppConfig) { c.Shutdown.Timeout = -time.Second }, want: "shutdown.timeout"},
		{name: "drain", mutate: func(c *AppConfig) { c.Shutdown.DrainDelay = -time.Second }, want: "shutdown.drain_delay"},
		{name: "drain budget", mutate: func(c *AppConfig) {
			c.Shutdown.Graceful = true
			c.Shutdown.Timeout = time.Second
			c.Shutdown.DrainDelay = time.Second
		}, want: "less than shutdown.timeout"},
		{name: "http headers", mutate: func(c *AppConfig) { c.HttpServer.MaxHeaderBytes = -1 }, want: "max_header_bytes"},
		{name: "grpc receive", mutate: func(c *AppConfig) { c.GrpcServer.MaxRecvMessageBytes = -1 }, want: "message size"},
		{name: "grpc keepalive", mutate: func(c *AppConfig) { c.GrpcServer.KeepaliveTime = -time.Second }, want: "keepalive_time"},
		{name: "datasource interval", mutate: func(c *AppConfig) { c.DatasourceMonitor.Interval = -time.Second }, want: "datasource_monitor.interval"},
		{name: "datasource timeout", mutate: func(c *AppConfig) { c.DatasourceMonitor.Timeout = -time.Second }, want: "datasource_monitor.timeout"},
		{name: "datasource stale", mutate: func(c *AppConfig) { c.DatasourceMonitor.StaleAfter = -time.Second }, want: "datasource_monitor.stale_after"},
		{name: "datasource SQL capacity", mutate: func(c *AppConfig) { c.DatasourceMonitor.SQLCapacity = -1 }, want: "sql_capacity"},
		{name: "datasource slow capacity", mutate: func(c *AppConfig) { c.DatasourceMonitor.SlowCapacity = -1 }, want: "slow_capacity"},
		{name: "datasource slow threshold", mutate: func(c *AppConfig) { c.DatasourceMonitor.SlowSQLThreshold = -time.Second }, want: "slow_sql_threshold"},
		{name: "datasource stale interval", mutate: func(c *AppConfig) {
			c.DatasourceMonitor.Interval = 10 * time.Second
			c.DatasourceMonitor.StaleAfter = 10 * time.Second
		}, want: "greater than interval"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := AppConfig{}
			test.mutate(&config)
			assert.ErrorContains(t, validateRuntimeConfig(config), test.want)
		})
	}
	assert.NoError(t, validateRuntimeConfig(AppConfig{Shutdown: ShutdownConfig{
		Graceful: false, Timeout: time.Second, DrainDelay: time.Second,
	}}), "drain budget is unused when graceful shutdown is disabled")
	assert.NoError(t, validateRuntimeConfig(AppConfig{}))
}

// secretNamePattern matches configuration field names that carry credential
// material. Fields whose name ends in File hold a path to the material rather
// than the material itself, so they stay readable.
var secretNamePattern = regexp.MustCompile(`(?i)(key|secret|password|token|credential)`)

// secretTagExemptions lists credential-shaped names that are deliberately
// published. An S3 access key is an identifier paired with the redacted secret
// key, matching the convention used by object-store SDKs.
var secretTagExemptions = map[string]struct{}{
	"AppConfig.IAMAudit.Anchor.AccessKey": {},
}

// TestCredentialFieldsAreMarkedSecret keeps /debug/config from echoing key
// material: every credential-shaped string reachable from AppConfig must carry
// secret:"true" so configx.Inspect redacts it.
func TestCredentialFieldsAreMarkedSecret(t *testing.T) {
	visited := make(map[reflect.Type]bool)

	var walk func(typ reflect.Type, path string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice ||
			typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || visited[typ] {
			return
		}
		visited[typ] = true

		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			if !field.IsExported() {
				continue
			}
			fieldPath := path + "." + field.Name
			_, exempt := secretTagExemptions[fieldPath]
			if field.Type.Kind() == reflect.String &&
				!strings.HasSuffix(field.Name, "File") &&
				!exempt &&
				secretNamePattern.MatchString(field.Name) {
				assert.Equal(t, "true", field.Tag.Get("secret"),
					"%s holds credential material and must be tagged secret:\"true\"", fieldPath)
			}
			walk(field.Type, fieldPath)
		}
	}

	walk(reflect.TypeOf(AppConfig{}), "AppConfig")
}

// TestInspectRedactsAuthnKeyMaterial pins the observable effect: the signing
// seed and the MFA encryption key never reach the published configuration.
func TestInspectRedactsAuthnKeyMaterial(t *testing.T) {
	const (
		signingSeed   = "dGVzdC1zaWduaW5nLXNlZWQtbm90LXJlYWw="
		encryptionKey = "dGVzdC1lbmNyeXB0aW9uLWtleS1ub3QtcmVhbA=="
	)

	var parsed AppConfig
	require.NoError(t, configx.New().Parse(&parsed,
		"--authn.signing_key="+signingSeed,
		"--authn.mfa.encryption_key="+encryptionKey,
	))
	require.Equal(t, signingSeed, parsed.Authn.SigningKey)
	require.Equal(t, encryptionKey, parsed.Authn.MFA.EncryptionKey)

	inspected, err := configx.Inspect(parsed)
	require.NoError(t, err)

	authn, ok := inspected["authn"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, configx.RedactedValue, authn["signing_key"])

	mfa, ok := authn["mfa"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, configx.RedactedValue, mfa["encryption_key"])

	rendered, err := json.Marshal(inspected)
	require.NoError(t, err)
	assert.NotContains(t, string(rendered), signingSeed)
	assert.NotContains(t, string(rendered), encryptionKey)
}

// TestAuditOverflowIsTrimmedAndValidated stops a typo or a stray space from
// silently degrading a blocking audit queue into a dropping one.
func TestAuditOverflowIsTrimmedAndValidated(t *testing.T) {
	var defaults AppConfig
	require.NoError(t, configx.New().Parse(&defaults))
	assert.Equal(t, "drop", defaults.Audit.Overflow)

	var padded AppConfig
	require.NoError(t, configx.New().Parse(&padded, "--audit.overflow= block "))
	assert.Equal(t, "block", padded.Audit.Overflow)

	var invalid AppConfig
	require.Error(t, configx.New().Parse(&invalid, "--audit.overflow=weird"))
}
