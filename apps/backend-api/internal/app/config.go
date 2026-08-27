package app

type AppConfig struct {
	Name    string `mapstructure:"name" description:"application name" trim:"true"`
	Version string `mapstructure:"version" description:"application version" trim:"true"`

	Debug      bool                `mapstructure:"debug" short:"d" description:"debug mode" default:"false"`
	Timezone   string              `mapstructure:"timezone" description:"timezone" default:"UTC"`
	HttpServer HttpServer          `mapstructure:"http_server" group:"http_server"`
	GrpcServer GrpcServer          `mapstructure:"grpc_server" group:"grpc_server"`
	Database   map[string]Database `mapstructure:"database" group:"database" mapkey:"<dbkey>"`
}

type HttpServer struct {
	Address string `mapstructure:"address" description:"address" default:"0.0.0.0"`
	Port    int    `mapstructure:"port" description:"port" default:"8080"`
}

type GrpcServer struct {
	Address string `mapstructure:"address" description:"address" default:"0.0.0.0"`
	Port    int    `mapstructure:"port" description:"port" default:"9090"`
}

type Database struct {
	Type string `mapstructure:"type" description:"database type" default:"sqlite"`
	Dsn  string `mapstructure:"dsn" description:"data source name" default:"sqlite:///sqlite.db"`
}
