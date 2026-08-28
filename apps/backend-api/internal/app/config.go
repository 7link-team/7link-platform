package app

import (
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

	Timezone string `mapstructure:"timezone" description:"timezone" default:"UTC"`

	HttpServer HttpServer `mapstructure:"http_server" group:"http_server"`
	GrpcServer GrpcServer `mapstructure:"grpc_server" group:"grpc_server"`

	Migrate  bool                `mapstructure:"migrate" description:"automatically migrate before starting services" default:"false"`
	Database map[string]Database `mapstructure:"database" group:"database" mapkey:"<dbkey>"`
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
	Address string `mapstructure:"address" description:"listen IPv4 address" default:"0.0.0.0" trim:"true" required:"true" validate:"^((25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])[.]){3}(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])$"`
	Port    int    `mapstructure:"port" description:"port" default:"8080"`
}

type API struct {
	Landing         APILanding         `mapstructure:"landing" group:"landing"`
	Docs            APIDocs            `mapstructure:"docs" group:"docs"`
	OpenAPI         APIOpenAPI         `mapstructure:"openapi" group:"openapi"`
	Metrics         APIMetrics         `mapstructure:"metrics" group:"metrics"`
	MigrationStatus APIMigrationStatus `mapstructure:"migration_status" group:"migration_status"`
	ConfigInspector APIConfigInspector `mapstructure:"config_inspector" group:"config_inspector"`
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

type GrpcServer struct {
	Address string `mapstructure:"address" description:"listen IPv4 address" default:"0.0.0.0" trim:"true" required:"true" validate:"^((25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])[.]){3}(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])$"`
	Port    int    `mapstructure:"port" description:"port" default:"9090"`
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
