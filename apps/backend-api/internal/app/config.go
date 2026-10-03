package app

import (
	"fmt"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
)

type AppConfig struct {
	Name    string `mapstructure:"name" description:"application name" trim:"true" default:""`
	Version string `mapstructure:"version" description:"application version" trim:"true"`

	Debug bool `mapstructure:"debug" short:"d" description:"debug mode" default:"false"`
	// Deployment is what people signing in see. Name above is what operators
	// see -- in logs and on the banner -- and the two are deliberately separate:
	// a service called iam-api may well front a console called something else.
	Deployment   DeploymentConfig `mapstructure:"deployment" group:"deployment"`
	Banner       BannerConfig     `mapstructure:"banner" group:"banner"`
	Pprof        Pprof            `mapstructure:"pprof" group:"pprof"`
	API          API              `mapstructure:"api" group:"api"`
	Telemetry    Telemetry        `mapstructure:"telemetry" group:"telemetry"`
	Audit        Audit            `mapstructure:"audit" group:"audit"`
	Authn        IAMAuthn         `mapstructure:"authn" group:"authn"`
	IAM          IAMCore          `mapstructure:"iam" group:"iam"`
	Files        FilesConfig      `mapstructure:"files" group:"files"`
	IAMAudit     IAMAudit         `mapstructure:"iam_audit" group:"iam_audit"`
	Provisioning IAMProvisioning  `mapstructure:"provisioning" group:"provisioning"`
	Federation   IAMFederation    `mapstructure:"federation" group:"federation"`
	Webhook      IAMWebhook       `mapstructure:"webhook" group:"webhook"`
	OAuth        IAMOAuth         `mapstructure:"oauth" group:"oauth"`
	Shutdown     ShutdownConfig   `mapstructure:"shutdown" group:"shutdown"`

	Timezone string `mapstructure:"timezone" description:"timezone" default:"UTC"`

	HttpServer HttpServer `mapstructure:"http_server" group:"http_server"`
	GrpcServer GrpcServer `mapstructure:"grpc_server" group:"grpc_server"`

	Migrate           bool                `mapstructure:"migrate" description:"automatically migrate before starting services" default:"false"`
	TablePrefix       string              `mapstructure:"table_prefix" description:"prefix prepended to every table this application owns" validate:"^$|^[a-z_][a-z0-9_]{0,20}$"`
	Database          map[string]Database `mapstructure:"database" group:"database" mapkey:"<dbkey>"`
	DatasourceMonitor DatasourceMonitor   `mapstructure:"datasource_monitor" group:"datasource_monitor"`
}

// DeploymentConfig names this deployment for the people using its console. A
// build hardcodes neither: they describe whoever deployed it, and a deployment
// that configures neither is served neutral wording by the client.
type DeploymentConfig struct {
	Name         string `mapstructure:"name" description:"console display name shown to people signing in; empty leaves the console's neutral wording" default:""`
	Organization string `mapstructure:"organization" description:"organization named in the console footer; empty shows only the year" default:""`
}

// IAMAuthn is the application-owned configuration surface for the shared
// authentication extension. A distinct type keeps AppConfig ownership here
// while preserving the extension's complete field and validation contract.
type IAMAuthn authnext.Config

// IAMCore configures platform-wide IAM policy that belongs to no single IAM
// sub-domain.
type IAMCore struct {
	TenantRegistrationEnabled bool `mapstructure:"tenant_registration_enabled" description:"allow verified principals to register their own tenants" default:"false"`
	// RequirePlatformAdministrator defaults to true because a freshly migrated
	// database can serve no platform operation at all. Recovery and migration
	// tooling can turn it off to reach an otherwise unreachable deployment.
	RequirePlatformAdministrator bool `mapstructure:"require_platform_administrator" description:"report IAM unready until a platform administrator exists" default:"true"`
}

// FilesConfig owns storage settings shared by all file consumers.
type FilesConfig struct {
	LocalRoot        string            `mapstructure:"local_root" description:"final local file storage root" default:"data/files"`
	LegacyLocalRoots map[string]string `mapstructure:"legacy_local_roots" group:"legacy_local_roots" mapkey:"<key-prefix>" description:"migration-only mapping from legacy key prefixes to source local roots"`
}

// IAMAudit configures the IAM domain audit chain. It is intentionally separate
// from Audit, which controls transport-level operation logging.
type IAMAudit struct {
	Anchor        IAMAuditAnchor        `mapstructure:"anchor" group:"anchor"`
	Archive       IAMAuditArchive       `mapstructure:"archive" group:"archive"`
	ErasureLedger IAMAuditErasureLedger `mapstructure:"erasure_ledger" group:"erasure_ledger"`
}

type IAMAuditErasureLedger struct {
	Enabled bool          `mapstructure:"enabled" description:"require an external immutable obligation before permanently erasing a principal" default:"false"`
	Timeout time.Duration `mapstructure:"timeout" description:"maximum duration of each synchronous erasure-ledger operation" default:"10s"`
}

type IAMAuditArchive struct {
	Enabled  bool          `mapstructure:"enabled" description:"execute durable audit archives using the anchor S3 configuration; source events are never deleted" default:"false"`
	Interval time.Duration `mapstructure:"interval" description:"interval between archive sweeps; at least one second" default:"1m"`
	Timeout  time.Duration `mapstructure:"timeout" description:"maximum duration per archive attempt; one second to one hour" default:"4m"`
}

type IAMAuditAnchor struct {
	Enabled       bool   `mapstructure:"enabled" description:"anchor verified IAM audit heads in an external object-lock store" default:"false"`
	Endpoint      string `mapstructure:"endpoint" description:"S3-compatible audit anchor endpoint" default:""`
	Bucket        string `mapstructure:"bucket" description:"bucket holding IAM audit anchors" default:"audit-anchors"`
	Region        string `mapstructure:"region" description:"audit anchor signing region" default:""`
	AccessKey     string `mapstructure:"access_key" description:"S3 audit anchor access key" default:""`
	SecretKey     string `mapstructure:"secret_key" description:"S3 audit anchor secret key" default:"" secret:"true"`
	RetentionDays int    `mapstructure:"retention_days" description:"audit anchor object-lock retention in days" default:"365"`
	SigningKey    string `mapstructure:"signing_key" description:"base64 Ed25519 seed signing IAM audit roots" default:"" secret:"true"`
}

type IAMProvisioning struct {
	EncryptionKey          string        `mapstructure:"encryption_key" description:"base64 key encrypting outbound SCIM credentials" default:"" secret:"true"`
	EncryptionKeyFile      string        `mapstructure:"encryption_key_file" description:"file containing the outbound SCIM encryption key" default:""`
	HTTPTimeout            time.Duration `mapstructure:"http_timeout" description:"outbound SCIM request timeout" default:"10s"`
	OutboundAllowPlaintext bool          `mapstructure:"outbound_allow_plaintext" description:"permit http:// SCIM targets; leave false to require https" default:"false"`
	OutboundAllowNetworks  []string      `mapstructure:"outbound_allow_networks" description:"CIDR blocks a SCIM target may resolve to despite being private or reserved"`
	SyncInterval           time.Duration `mapstructure:"sync_interval" description:"outbound SCIM reconciliation interval; zero disables it" default:"0"`
}

// IAMOAuth configures the parts of the OAuth authorization server an operator
// can move: where the consent screen lives and how long an unanswered request
// stays open.
type IAMOAuth struct {
	Consent IAMOAuthConsent `mapstructure:"consent" group:"consent"`
}

type IAMOAuthConsent struct {
	Path         string        `mapstructure:"path" description:"path the authorization endpoint sends a browser to for consent; point it at your own screen to replace the built-in one" trim:"true" default:"/oauth/consent" validate:"^/[A-Za-z0-9._~!$&'()*+,;=:@%/-]*$"`
	TemplateFile string        `mapstructure:"template_file" description:"HTML template replacing the built-in consent screen" trim:"true" default:""`
	TTL          time.Duration `mapstructure:"ttl" description:"how long an authorization request stays answerable on the consent screen" default:"10m"`
}

// IAMWebhook configures outbound event delivery. It follows each tenant's audit
// chain, so enabling it adds no instrumentation anywhere else; what it may reach
// is bounded here rather than by whatever a tenant types into a receiver URL.
type IAMWebhook struct {
	Enabled                    bool          `mapstructure:"enabled" description:"deliver tenant events to registered receivers" default:"false"`
	EncryptionKey              string        `mapstructure:"encryption_key" description:"base64 32-byte key protecting subscription signing secrets" default:"" secret:"true"`
	EncryptionKeyFile          string        `mapstructure:"encryption_key_file" description:"file containing the webhook encryption key" default:""`
	PreviousEncryptionKeys     []string      `mapstructure:"previous_encryption_keys" description:"retired webhook keys that still open stored secrets" secret:"true"`
	PreviousEncryptionKeyFiles []string      `mapstructure:"previous_encryption_key_files" description:"files containing retired webhook keys"`
	OutboundAllowPlaintext     bool          `mapstructure:"outbound_allow_plaintext" description:"permit http:// receivers; leave false to require https" default:"false"`
	OutboundAllowNetworks      []string      `mapstructure:"outbound_allow_networks" description:"CIDR blocks a receiver may resolve to despite being private or reserved"`
	HTTPTimeout                time.Duration `mapstructure:"http_timeout" description:"receiver request timeout" default:"10s"`
	PollInterval               time.Duration `mapstructure:"poll_interval" description:"how often the chain is followed and due deliveries are sent" default:"5s"`
	RetryBase                  time.Duration `mapstructure:"retry_base" description:"first retry delay; each further attempt doubles it" default:"5s"`
	MaxAttempts                int           `mapstructure:"max_attempts" description:"attempts before a delivery is abandoned" default:"8"`
	DeliveryBatch              int           `mapstructure:"delivery_batch" description:"deliveries attempted in one pass" default:"50"`
	FanOutBatch                int           `mapstructure:"fan_out_batch" description:"events read from one tenant's chain in one pass" default:"200"`
	FanOutSubscriptions        int           `mapstructure:"fan_out_subscriptions" description:"subscriptions caught up in one pass" default:"200"`
}

type IAMFederation struct {
	Enabled                bool              `mapstructure:"enabled" description:"enable inbound identity-provider federation" default:"false"`
	EncryptionKey          string            `mapstructure:"encryption_key" description:"base64 key protecting federation secrets and state" default:"" secret:"true"`
	EncryptionKeyFile      string            `mapstructure:"encryption_key_file" description:"file containing the federation encryption key" default:""`
	HTTPTimeout            time.Duration     `mapstructure:"http_timeout" description:"federation upstream HTTP timeout" default:"10s"`
	OutboundAllowPlaintext bool              `mapstructure:"outbound_allow_plaintext" description:"permit http:// upstream identity providers; leave false to require https" default:"false"`
	OutboundAllowNetworks  []string          `mapstructure:"outbound_allow_networks" description:"CIDR blocks an upstream identity provider may resolve to despite being private or reserved"`
	ClockSkew              time.Duration     `mapstructure:"clock_skew" description:"allowed upstream token clock skew" default:"30s"`
	StateTTL               time.Duration     `mapstructure:"state_ttl" description:"federation login state lifetime" default:"10m"`
	SAML                   IAMFederationSAML `mapstructure:"saml" group:"saml"`
}

type IAMFederationSAML struct {
	Enabled         bool          `mapstructure:"enabled" description:"enable the SAML identity provider" default:"false"`
	SigningKeyFile  string        `mapstructure:"signing_key_file" description:"PEM RSA private key used to sign SAML assertions" default:""`
	CertificateFile string        `mapstructure:"certificate_file" description:"PEM certificate matching the SAML signing key" default:""`
	LoginPath       string        `mapstructure:"login_path" description:"browser login path for unauthenticated SAML requests" default:"/login"`
	ValidDuration   time.Duration `mapstructure:"valid_duration" description:"SAML assertion and metadata validity window" default:"5m"`
	MaxIssueDelay   time.Duration `mapstructure:"max_issue_delay" description:"maximum accepted SAML request age" default:"5m"`
}

type BannerConfig struct {
	Enabled bool   `mapstructure:"enabled" description:"print the startup banner" default:"true"`
	Text    string `mapstructure:"text" description:"banner text; defaults to the application name" trim:"true"`
	Type    string `mapstructure:"type" description:"Figurine font, for example: standard, slant, small, block, shadow, doom, starwars" trim:"true" default:"standard"`
	Theme   string `mapstructure:"theme" description:"banner color theme: rainbow | spring | ocean | sunset | fire | plain" trim:"true" default:"rainbow" validate:"^(rainbow|spring|ocean|sunset|fire|plain)$"`
}

type Pprof struct {
	Enabled bool `mapstructure:"enabled" description:"enable runtime profiling endpoints" default:"false"`
}

// Shutdown controls how protocol servers and modules stop after a shutdown
// signal or fatal serve error.
type ShutdownConfig struct {
	Graceful   bool          `mapstructure:"graceful" description:"gracefully drain protocol servers before shutdown" default:"true"`
	Timeout    time.Duration `mapstructure:"timeout" description:"maximum time allowed for shutdown cleanup" default:"30s"`
	DrainDelay time.Duration `mapstructure:"drain_delay" description:"delay after readiness is disabled before listeners stop" default:"5s"`
}

type HttpServer struct {
	Address           string        `mapstructure:"address" description:"listen IPv4 address" default:"0.0.0.0" trim:"true" required:"true" validate:"^((25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])[.]){3}(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])$"`
	Port              int           `mapstructure:"port" description:"port" default:"8080"`
	ReadHeaderTimeout time.Duration `mapstructure:"read_header_timeout" description:"maximum time to read request headers" default:"5s"`
	IdleTimeout       time.Duration `mapstructure:"idle_timeout" description:"maximum idle keep-alive connection time" default:"2m"`
	MaxHeaderBytes    int           `mapstructure:"max_header_bytes" description:"maximum request header size in bytes" default:"1048576"`
}

type API struct {
	Landing             APILanding             `mapstructure:"landing" group:"landing"`
	Docs                APIDocs                `mapstructure:"docs" group:"docs"`
	OpenAPI             APIOpenAPI             `mapstructure:"openapi" group:"openapi"`
	Metrics             APIMetrics             `mapstructure:"metrics" group:"metrics"`
	MigrationStatus     APIMigrationStatus     `mapstructure:"migration_status" group:"migration_status"`
	ConfigInspector     APIConfigInspector     `mapstructure:"config_inspector" group:"config_inspector"`
	DatasourceInspector APIDatasourceInspector `mapstructure:"datasource_inspector" group:"datasource_inspector"`
}

type APILanding struct {
	Enabled         bool `mapstructure:"enabled" description:"enable the API root landing endpoint" default:"true"`
	AdvertiseHealth bool `mapstructure:"advertise_health" description:"show liveness and readiness links on the API landing endpoint" default:"false"`
}

type APIDocs struct {
	Enabled   bool `mapstructure:"enabled" description:"enable interactive API documentation and its assets" default:"true"`
	Advertise bool `mapstructure:"advertise" description:"show the API documentation link on the API landing endpoint" default:"true"`
}

type APIOpenAPI struct {
	Enabled   bool `mapstructure:"enabled" description:"enable OpenAPI JSON and YAML endpoints" default:"true"`
	Advertise bool `mapstructure:"advertise" description:"show the OpenAPI specification link on the API landing endpoint" default:"true"`
}

type APIMetrics struct {
	Enabled   bool `mapstructure:"enabled" description:"enable the Prometheus metrics endpoint" default:"false"`
	Advertise bool `mapstructure:"advertise" description:"show the Prometheus metrics link on the API landing endpoint" default:"false"`
}

type APIMigrationStatus struct {
	Enabled   bool `mapstructure:"enabled" description:"enable the read-only migration status endpoint" default:"false"`
	Advertise bool `mapstructure:"advertise" description:"show migration status on the API landing endpoint" default:"false"`
}

type APIConfigInspector struct {
	Enabled   bool `mapstructure:"enabled" description:"enable the redacted effective configuration inspector" default:"false"`
	Advertise bool `mapstructure:"advertise" description:"show the configuration inspector on the API landing endpoint" default:"false"`
}

type APIDatasourceInspector struct {
	Enabled   bool `mapstructure:"enabled" description:"enable the read-only datasource diagnostics endpoint" default:"false"`
	Advertise bool `mapstructure:"advertise" description:"show datasource diagnostics on the API landing endpoint" default:"false"`
}

type DatasourceMonitor struct {
	Interval         time.Duration `mapstructure:"interval" description:"interval between datasource health probes" default:"10s"`
	Timeout          time.Duration `mapstructure:"timeout" description:"maximum duration of one datasource health probe" default:"2s"`
	StaleAfter       time.Duration `mapstructure:"stale_after" description:"maximum age of a datasource health snapshot" default:"30s"`
	SQLCapacity      int           `mapstructure:"sql_capacity" description:"maximum number of SQL fingerprints retained in memory" default:"500"`
	SlowCapacity     int           `mapstructure:"slow_capacity" description:"maximum number of slow SQL samples retained in memory" default:"200"`
	SlowSQLThreshold time.Duration `mapstructure:"slow_sql_threshold" description:"minimum execution duration recorded as slow SQL" default:"200ms"`
}

// Telemetry is the application-owned telemetry configuration. It stays inside
// this package so AppConfig never depends on module packages; the telemetry
// module's runtime configuration is derived from it at construction time.
type Telemetry struct {
	OTLP TelemetryOTLP `mapstructure:"otlp" group:"otlp"`
}

// TelemetryOTLP mirrors the telemetry module's OTLP exporter settings.
type TelemetryOTLP struct {
	Enabled  bool              `mapstructure:"enabled" description:"export traces to an OTLP collector" default:"false"`
	Protocol string            `mapstructure:"protocol" description:"OTLP transport: grpc | http/protobuf" default:"grpc" trim:"true"`
	Endpoint string            `mapstructure:"endpoint" description:"OTLP collector host and port" trim:"true"`
	Insecure bool              `mapstructure:"insecure" description:"disable TLS for the OTLP connection" default:"false"`
	Headers  map[string]string `mapstructure:"headers" group:"headers" mapkey:"<header>" description:"OTLP request headers" secret:"true"`
	Timeout  time.Duration     `mapstructure:"timeout" description:"OTLP export timeout" default:"10s"`
}

// Audit is the application-owned operation audit configuration.
type Audit struct {
	Enabled         bool          `mapstructure:"enabled" description:"enable operation audit logging" default:"true"`
	Database        AuditDatabase `mapstructure:"database" group:"database"`
	RecordReads     bool          `mapstructure:"record_reads" description:"record read-only operations" default:"false"`
	RecordStreams   bool          `mapstructure:"record_streams" description:"record WebSocket and SSE sessions" default:"true"`
	CaptureBodies   bool          `mapstructure:"capture_bodies" description:"capture redacted JSON request and error response bodies" default:"false"`
	MaxBodyBytes    int           `mapstructure:"max_body_bytes" description:"maximum captured request or response body size in bytes" default:"8192"`
	SensitiveFields []string      `mapstructure:"sensitive_fields" description:"additional JSON field names to redact" default:"[]"`
	SkipRoutes      []string      `mapstructure:"skip_routes" description:"additional request path prefixes excluded from auditing" default:"[]"`
	QueueSize       int           `mapstructure:"queue_size" description:"audit event queue capacity" default:"4096"`
	BatchSize       int           `mapstructure:"batch_size" description:"events per database write batch" default:"200"`
	FlushInterval   time.Duration `mapstructure:"flush_interval" description:"maximum time between database flushes" default:"1s"`
	FlushTimeout    time.Duration `mapstructure:"flush_timeout" description:"maximum time for one database flush" default:"5s"`
	Overflow        string        `mapstructure:"overflow" description:"queue overflow behavior: drop | block" default:"drop" trim:"true" validate:"^(drop|block)$"`
}

// AuditDatabase gates the database-backed audit sink.
type AuditDatabase struct {
	Enabled bool `mapstructure:"enabled" description:"persist audit events to the operation_logs table" default:"false"`
}

type GrpcServer struct {
	Address               string        `mapstructure:"address" description:"listen IPv4 address" default:"0.0.0.0" trim:"true" required:"true" validate:"^((25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])[.]){3}(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])$"`
	Port                  int           `mapstructure:"port" description:"port" default:"9090"`
	Reflection            bool          `mapstructure:"reflection" description:"enable gRPC server reflection" default:"false"`
	MaxRecvMessageBytes   int           `mapstructure:"max_recv_message_bytes" description:"maximum received gRPC message size; zero uses the library default" default:"0"`
	MaxSendMessageBytes   int           `mapstructure:"max_send_message_bytes" description:"maximum sent gRPC message size; zero uses the library default" default:"0"`
	MaxConnectionIdle     time.Duration `mapstructure:"max_connection_idle" description:"maximum idle gRPC connection time; zero uses the library default" default:"0s"`
	MaxConnectionAge      time.Duration `mapstructure:"max_connection_age" description:"maximum gRPC connection age; zero uses the library default" default:"0s"`
	MaxConnectionAgeGrace time.Duration `mapstructure:"max_connection_age_grace" description:"grace period after maximum gRPC connection age" default:"0s"`
	KeepaliveTime         time.Duration `mapstructure:"keepalive_time" description:"server keepalive ping interval; zero uses the library default" default:"0s"`
	KeepaliveTimeout      time.Duration `mapstructure:"keepalive_timeout" description:"server keepalive ping timeout; zero uses the library default" default:"0s"`
	KeepaliveMinTime      time.Duration `mapstructure:"keepalive_min_time" description:"minimum accepted client keepalive interval; zero uses the library default" default:"0s"`
	PermitWithoutStream   bool          `mapstructure:"permit_without_stream" description:"allow client keepalive pings without active RPCs" default:"false"`
}

type Database struct {
	Type string `mapstructure:"type" description:"database type: sqlite | mysql | postgres"`
	Dsn  string `mapstructure:"dsn" description:"data source name" secret:"true"`

	Writable bool `mapstructure:"writable" description:"route write operations to this datasource" default:"true"`
	Readable bool `mapstructure:"readable" description:"route read operations to this datasource" default:"true"`

	MaxOpenConns    int           `mapstructure:"max_open_conns" description:"maximum open database connections" default:"25"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns" description:"maximum idle database connections" default:"5"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime" description:"maximum connection lifetime" default:"30m"`
	ConnMaxIdleTime time.Duration `mapstructure:"conn_max_idle_time" description:"maximum connection idle time" default:"5m"`
}

const (
	defaultHTTPReadHeaderTimeout   = 5 * time.Second
	defaultHTTPIdleTimeout         = 2 * time.Minute
	defaultHTTPMaxHeaderBytes      = 1 << 20
	defaultShutdownTimeout         = 30 * time.Second
	defaultDrainDelay              = 5 * time.Second
	defaultDatasourceProbeInterval = 10 * time.Second
	defaultDatasourceProbeTimeout  = 2 * time.Second
	defaultDatasourceStaleAfter    = 30 * time.Second
	defaultDatasourceSQLCapacity   = 500
	defaultDatasourceSlowCapacity  = 200
	defaultSlowSQLThreshold        = 200 * time.Millisecond
)

// applyRuntimeDefaults protects callers that construct AppConfig directly and
// therefore do not receive tagged configuration defaults.
func applyRuntimeDefaults(config *AppConfig) {
	if config == nil {
		return
	}
	if config.HttpServer.ReadHeaderTimeout == 0 {
		config.HttpServer.ReadHeaderTimeout = defaultHTTPReadHeaderTimeout
	}
	if config.HttpServer.IdleTimeout == 0 {
		config.HttpServer.IdleTimeout = defaultHTTPIdleTimeout
	}
	if config.HttpServer.MaxHeaderBytes == 0 {
		config.HttpServer.MaxHeaderBytes = defaultHTTPMaxHeaderBytes
	}
	if config.Shutdown.Timeout == 0 {
		config.Shutdown.Timeout = defaultShutdownTimeout
	}
	if config.Shutdown.DrainDelay == 0 {
		config.Shutdown.DrainDelay = defaultDrainDelay
	}
	if config.DatasourceMonitor.Interval == 0 {
		config.DatasourceMonitor.Interval = defaultDatasourceProbeInterval
	}
	if config.DatasourceMonitor.Timeout == 0 {
		config.DatasourceMonitor.Timeout = defaultDatasourceProbeTimeout
	}
	if config.DatasourceMonitor.StaleAfter == 0 {
		config.DatasourceMonitor.StaleAfter = defaultDatasourceStaleAfter
	}
	if config.DatasourceMonitor.SQLCapacity == 0 {
		config.DatasourceMonitor.SQLCapacity = defaultDatasourceSQLCapacity
	}
	if config.DatasourceMonitor.SlowCapacity == 0 {
		config.DatasourceMonitor.SlowCapacity = defaultDatasourceSlowCapacity
	}
	if config.DatasourceMonitor.SlowSQLThreshold == 0 {
		config.DatasourceMonitor.SlowSQLThreshold = defaultSlowSQLThreshold
	}
}

func validateRuntimeConfig(config AppConfig) error {
	// The in-process limiter store exists so a developer can watch the limiter
	// work without standing up Redis. It is a test double reachable by this
	// process alone, and it must never be what a deployment is limiting
	// against, so debug mode is the price of asking for it. This is the only
	// place both the request and the debug flag are visible, which is why the
	// refusal lives here rather than in the module that honors it.
	if config.Authn.SharedSecurity.DevelopmentServer && !config.Debug {
		return fmt.Errorf("authn.shared_security.development_server requires debug mode")
	}
	if config.Shutdown.Timeout < 0 {
		return fmt.Errorf("shutdown.timeout cannot be negative")
	}
	if config.Shutdown.DrainDelay < 0 {
		return fmt.Errorf("shutdown.drain_delay cannot be negative")
	}
	if config.Shutdown.Graceful && config.Shutdown.Timeout > 0 && config.Shutdown.DrainDelay >= config.Shutdown.Timeout {
		return fmt.Errorf("shutdown.drain_delay must be less than shutdown.timeout")
	}
	if config.HttpServer.ReadHeaderTimeout < 0 {
		return fmt.Errorf("http_server.read_header_timeout cannot be negative")
	}
	if config.HttpServer.IdleTimeout < 0 {
		return fmt.Errorf("http_server.idle_timeout cannot be negative")
	}
	if config.HttpServer.MaxHeaderBytes < 0 {
		return fmt.Errorf("http_server.max_header_bytes cannot be negative")
	}
	if config.DatasourceMonitor.Interval < 0 {
		return fmt.Errorf("datasource_monitor.interval cannot be negative")
	}
	if config.DatasourceMonitor.Timeout < 0 {
		return fmt.Errorf("datasource_monitor.timeout cannot be negative")
	}
	if config.DatasourceMonitor.StaleAfter < 0 {
		return fmt.Errorf("datasource_monitor.stale_after cannot be negative")
	}
	if config.DatasourceMonitor.StaleAfter > 0 && config.DatasourceMonitor.Interval > 0 &&
		config.DatasourceMonitor.StaleAfter <= config.DatasourceMonitor.Interval {
		return fmt.Errorf("datasource_monitor.stale_after must be greater than interval")
	}
	if config.DatasourceMonitor.SQLCapacity < 0 {
		return fmt.Errorf("datasource_monitor.sql_capacity cannot be negative")
	}
	if config.DatasourceMonitor.SlowCapacity < 0 {
		return fmt.Errorf("datasource_monitor.slow_capacity cannot be negative")
	}
	if config.DatasourceMonitor.SlowSQLThreshold < 0 {
		return fmt.Errorf("datasource_monitor.slow_sql_threshold cannot be negative")
	}
	if config.GrpcServer.MaxRecvMessageBytes < 0 || config.GrpcServer.MaxSendMessageBytes < 0 {
		return fmt.Errorf("gRPC message size limits cannot be negative")
	}
	grpcDurations := []struct {
		name  string
		value time.Duration
	}{
		{name: "max_connection_idle", value: config.GrpcServer.MaxConnectionIdle},
		{name: "max_connection_age", value: config.GrpcServer.MaxConnectionAge},
		{name: "max_connection_age_grace", value: config.GrpcServer.MaxConnectionAgeGrace},
		{name: "keepalive_time", value: config.GrpcServer.KeepaliveTime},
		{name: "keepalive_timeout", value: config.GrpcServer.KeepaliveTimeout},
		{name: "keepalive_min_time", value: config.GrpcServer.KeepaliveMinTime},
	}
	for _, setting := range grpcDurations {
		if setting.value < 0 {
			return fmt.Errorf("grpc_server.%s cannot be negative", setting.name)
		}
	}
	return nil
}
