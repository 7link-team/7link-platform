package cmd

import (
	"os"

	"7link.com/platform/internal/app"
	"7link.com/platform/pkg/config"
	"github.com/spf13/cobra"
)

var (
	buildVersion      = "dev"
	serveConfigurator = config.New()
	serveConfig       app.AppConfig
	serveConfigErr    error

	// serveCmd represents the serve command.
	serveCmd = &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP and gRPC servers",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if serveConfigErr != nil {
				return serveConfigErr
			}
			if err := serveConfigurator.Load(&serveConfig); err != nil {
				return err
			}
			applyBuildVersion(&serveConfig)
			return (&app.App{}).Run(serveConfig)
		},
	}
)

func init() {
	rootCmd.AddCommand(serveCmd)
	serveConfigurator.UseFlags(serveCmd.Flags())
	serveConfigurator.UseConfigFileArgDefault()
	serveConfigErr = serveConfigurator.Bind(&serveConfig, os.Args[1:]...)
}

func applyBuildVersion(config *app.AppConfig) {
	if config.Version == "" {
		config.Version = buildVersion
	}
}
