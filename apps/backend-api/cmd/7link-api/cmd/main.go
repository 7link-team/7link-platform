package cmd

import (
	"log"
	"os"

	"7link.com/platform/internal/app"
	"7link.com/platform/pkg/config"
	"github.com/spf13/cobra"
)

var (
	f = config.New()
	c = &app.App{}
	// cfg receives the parsed flags/env/config file. App itself has no exported
	// fields for Flagger to bind, so parsing must target the config struct.
	cfg = app.AppConfig{}
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "7link-api",
	Short: "7link API",
	Long:  `7link API`,
	Run: func(cmd *cobra.Command, args []string) {
		log.Printf("%+v\n", cfg)
		if err := c.Run(cfg); err != nil {
			log.Fatal(err)
		}
	},
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	f.UseFlags(rootCmd.Flags())
	f.UseConfigFileArgDefault()
	f.Parse(&cfg)
}
