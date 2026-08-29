package app

import (
	"fmt"
	"time"

	telemetry "7link.com/platform/internal/infra/telemetry"
)

type AppConfig struct {
	Name    string `mapstructure:"name" description:"application name" trim:"true" default:"7link-api"`
	Version string `mapstructure:"version" description:"application version" trim:"true"`

	Debug     bool             `mapstructure:"debug" short:"d" description:"debug mode" default:"false"`
	Banner    BannerConfig     `mapstructure:"banner" group:"banner"`
	Pprof     Pprof            `mapstructure:"pprof" group:"pprof"`
	API       API              `mapstructure:"api" group:"api"`
	Telemetry telemetry.Config `mapstructure:"telemetry" group:"telemetry"`

	Timezone        string        `mapstructure:"timezone" description:"timezone" default:"UTC"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout" description:"maximum time allowed for graceful shutdown" default:"30s"`
	DrainDelay      time.Duration `mapstructure:"drain_delay" description:"delay after readiness is disabled before listeners stop" default:"5s"`

	HttpServer HttpServer `mapstructure:"http_server" group:"http_server"`
	GrpcServer GrpcServer `mapstructure:"grpc_server" group:"grpc_server"`

	Migrate           bool                `mapstructure:"migrate" description:"automatically migrate before starting services" default:"false"`
	Database          map[string]Database `mapstructure:"database" group:"database" mapkey:"<dbkey>"`
	DatasourceMonitor DatasourceMonitor   `mapstructure:"datasource_monitor" group:"datasource_monitor"`
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
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = defaultShutdownTimeout
	}
	if config.DrainDelay == 0 {
		config.DrainDelay = defaultDrainDelay
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
	if config.ShutdownTimeout < 0 {
		return fmt.Errorf("shutdown_timeout cannot be negative")
	}
	if config.DrainDelay < 0 {
		return fmt.Errorf("drain_delay cannot be negative")
	}
	if config.ShutdownTimeout > 0 && config.DrainDelay >= config.ShutdownTimeout {
		return fmt.Errorf("drain_delay must be less than shutdown_timeout")
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
