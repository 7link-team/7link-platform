package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"7link.com/platform/internal/app"
	"7link.com/platform/pkg/config"
	"github.com/spf13/cobra"
)

// openapiConfig is the flag set this command carries. It is the ordinary
// application configuration plus the export's own settings, so the document
// describes the deployment as configured rather than an invented one.
type openapiConfig struct {
	app.AppConfig            `mapstructure:",squash"`
	app.OpenAPIExportRequest `mapstructure:",squash"`
}

var (
	openapiConfigurator  = config.New()
	openapiCommandConfig openapiConfig
	openapiConfigErr     error

	// openapiCmd writes the API document without starting a server. The
	// specification is otherwise only reachable from a running process, which
	// makes it useless to everything that runs before the service does: client
	// generators, compatibility checks, and the review of an API change.
	openapiCmd = &cobra.Command{
		Use:   "openapi",
		Short: "Write this deployment's OpenAPI document",
		Long: "Write this deployment's OpenAPI document without starting a server.\n\n" +
			"The document describes the application as configured, so a deployment that\n" +
			"runs fewer modules produces a smaller document. Nothing binds a port and no\n" +
			"request is served, but the modules are composed exactly as they are at\n" +
			"startup, which means the same configuration and the same database.\n\n" +
			"Use --out to write a file and --format to choose json (default) or yaml.\n" +
			"With no --out the document goes to standard output.\n\n" +
			"The version in the document comes from the configuration, never from the\n" +
			"build. A build stamp would change on every commit, so a checked-in document\n" +
			"could never match a fresh export.",
		Args: cobra.NoArgs,
		RunE: runOpenAPIExport,
	}
)

func init() {
	rootCmd.AddCommand(openapiCmd)
	openapiConfigurator.UseFlags(openapiCmd.Flags())
	openapiConfigurator.UseConfigFileArgDefault()
	openapiConfigErr = openapiConfigurator.Bind(&openapiCommandConfig, os.Args[1:]...)
}

func runOpenAPIExport(command *cobra.Command, _ []string) error {
	if openapiConfigErr != nil {
		return openapiConfigErr
	}
	if err := openapiConfigurator.Load(&openapiCommandConfig); err != nil {
		return err
	}
	document, err := (&app.App{}).RunOpenAPIExport(command.Context(), app.OpenAPIExportConfig{
		AppConfig:            openapiCommandConfig.AppConfig,
		OpenAPIExportRequest: openapiCommandConfig.OpenAPIExportRequest,
	})
	if err != nil {
		return withExitCode(openapiExitCode(err), err)
	}
	if openapiCommandConfig.Out == "" {
		_, err = command.OutOrStdout().Write(document)
		return err
	}
	if err := writeOpenAPIFile(openapiCommandConfig.Out, document); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "openapi: %s\n", openapiCommandConfig.Out)
	return nil
}

// writeOpenAPIFile replaces the target in one step. A half-written document is
// worse than no document: whatever reads it next -- a generator, a diff in
// continuous integration -- would report a difference that describes an
// interrupted command rather than a change to the API.
func writeOpenAPIFile(path string, document []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", directory, err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(document); err != nil {
		return errors.Join(fmt.Errorf("write %s: %w", name, err), temporary.Close())
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	// The document is not a secret, and a file only the writer can read would
	// be unusable by the build that has to consume it.
	if err := os.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("set permissions on %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// openapiExitCode separates "this deployment cannot answer right now" from an
// ordinary failure, so a build step can tell a missing database from a genuine
// problem with the API description.
func openapiExitCode(err error) int {
	if errors.Is(err, app.ErrOpenAPIExportUnavailable) {
		return exitTransient
	}
	return exitFailure
}
