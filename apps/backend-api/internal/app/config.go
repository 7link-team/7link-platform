package app

import "time"

type AppConfig struct {
	Name    string `mapstructure:"name" description:"application name" trim:"true"`
	Version string `mapstructure:"version" description:"application version" trim:"true"`

	Debug    bool   `mapstructure:"debug" short:"d" description:"debug mode" default:"false"`
	Timezone string `mapstructure:"timezone" description:"timezone" default:"UTC"`

	HttpServer HttpServer `mapstructure:"http_server" group:"http_server"`
	GrpcServer GrpcServer `mapstructure:"grpc_server" group:"grpc_server"`

	Database map[string]Database `mapstructure:"database" group:"database" mapkey:"<dbkey>"`
}

type HttpServer struct {
	Address string `mapstructure:"address" description:"listen IPv4 address" default:"0.0.0.0" trim:"true" required:"true" validate:"^((25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])[.]){3}(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])$"`
	Port    int    `mapstructure:"port" description:"port" default:"8080"`
}

type GrpcServer struct {
	Address string `mapstructure:"address" description:"listen IPv4 address" default:"0.0.0.0" trim:"true" required:"true" validate:"^((25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])[.]){3}(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])$"`
	Port    int    `mapstructure:"port" description:"port" default:"9090"`
}

type Database struct {
	Type string `mapstructure:"type" description:"database type: sqlite | mysql | postgres" default:"sqlite"`
	Dsn  string `mapstructure:"dsn" description:"data source name" default:"sqlite:///sqlite.db"`

	Writable bool `mapstructure:"writable" description:"route write operations to this datasource" default:"true"`
	Readable bool `mapstructure:"readable" description:"route read operations to this datasource" default:"true"`

	MaxOpenConns    int           `mapstructure:"max_open_conns" description:"maximum open database connections" default:"25"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns" description:"maximum idle database connections" default:"5"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime" description:"maximum connection lifetime" default:"30m"`
	ConnMaxIdleTime time.Duration `mapstructure:"conn_max_idle_time" description:"maximum connection idle time" default:"5m"`
}
