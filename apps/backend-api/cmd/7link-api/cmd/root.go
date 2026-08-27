package cmd

import (
	"os"

	"7link.com/platform/internal/app"
	"7link.com/platform/pkg/config"
	"github.com/spf13/cobra"
)

var (
	configurator = config.New()
	appRunner    = &app.App{}
	// cfg receives the parsed flags/env/config file. App itself has no exported
	// fields for Flagger to bind, so parsing must target the config struct.
	appConfig = app.AppConfig{}
	configErr error
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "7link-api",
	Short: "7link API",
	Long:  `7link API`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if configErr != nil {
			return configErr
		}
		return appRunner.Run(appConfig)
	},
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	configurator.UseFlags(rootCmd.Flags())
	configurator.UseConfigFileArgDefault()
	configErr = configurator.Parse(&appConfig)
}
