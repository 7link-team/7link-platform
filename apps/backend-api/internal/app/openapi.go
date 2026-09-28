package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	humax "7link.com/platform/pkg/extension/huma"
	slogx "7link.com/platform/pkg/extension/slog"
	"github.com/go-chi/chi/v5"
)

// OpenAPI document formats. Both describe the same document; the choice is
// about what reads it next, so neither is a default anyone has to argue with.
const (
	OpenAPIFormatJSON = "json"
	OpenAPIFormatYAML = "yaml"
)

// openAPIFallbackVersion stands in when the configuration names no version.
//
// The running service stamps its build into this field, which is right for
// something you ask "what exactly is deployed here". It is wrong for a document
// meant to be written to a file and compared: the version would change on every
// commit, so a checked-in specification could never match a fresh export, and
// the difference an operator actually wants to read -- what changed about the
// API -- would be buried under a line that changes for no reason.
const openAPIFallbackVersion = "0.0.0"

// ErrOpenAPIExportUnavailable marks a missing capability rather than a failure:
// an application whose modules never composed has no operations to describe,
// and an empty document is exactly the wrong thing to write over a good one.
var ErrOpenAPIExportUnavailable = errors.New("openapi export is unavailable")

// OpenAPIExportRequest carries the command's operation-only settings.
type OpenAPIExportRequest struct {
	// Out is where the document is written. Empty means standard output, so the
	// command composes with a pipe as readily as it writes a file.
	Out string `mapstructure:"out" description:"write the document to this file instead of standard output"`
	// Format selects the serialization. It is explicit rather than inferred from
	// the file extension: a rule that guesses is a rule somebody has to learn
	// before they can predict what a command will produce.
	Format string `mapstructure:"format" description:"document format, json or yaml"`
}

// OpenAPIExportConfig combines the normal application configuration with the
// command's own settings, the same way RewrapSecretsConfig does.
type OpenAPIExportConfig struct {
	AppConfig            `mapstructure:",squash"`
	OpenAPIExportRequest `mapstructure:",squash"`
}

// RunOpenAPIExport writes this deployment's OpenAPI document without starting
// any server.
//
// The specification is only served by a running process today, which makes it
// unusable as an input to anything that runs before the service does -- a client
// generator, a compatibility check, a review of what an API change did. This
// produces the same document from the same composition, with nothing listening
// on a port.
//
// It composes the application as configured rather than a fixed superset of it,
// because a document describing modules this deployment does not run would send
// a generator to build clients for endpoints that answer 404.
func (app *App) RunOpenAPIExport(
	ctx context.Context,
	config OpenAPIExportConfig,
) (document []byte, resultErr error) {
	format, err := normalizeOpenAPIFormat(config.Format)
	if err != nil {
		return nil, err
	}
	if err := configureApplication(&config.AppConfig); err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.AppConfig.Version) == "" {
		config.AppConfig.Version = openAPIFallbackVersion
	}
	// Standard output carries the document and nothing else, so redirecting it
	// yields a file a generator can read. Startup diagnostics are still worth
	// having -- they name the modules that shaped the document -- so they move
	// to standard error rather than being silenced.
	slog.SetDefault(slogx.NewConsole(
		processConsoleErrorWriter(), config.Debug, config.Name, nil))
	app.config = config.AppConfig
	app.ctx, app.cancel = context.WithCancel(ctx)
	defer func() {
		resultErr = errors.Join(resultErr, app.shutdown())
	}()
	if err := app.bootstrap(app.ctx); err != nil {
		return nil, err
	}
	if app.modules == nil {
		return nil, fmt.Errorf("%w: modules are not composed", ErrOpenAPIExportUnavailable)
	}

	// A throwaway router: the operations are registered against the API, and
	// nothing here binds an address or serves a request.
	api := humax.New(chi.NewRouter(), humax.Config{
		Name: app.config.Name, Version: app.config.Version,
	})
	if err := app.modules.RegisterHTTP(app.ctx, api); err != nil {
		return nil, err
	}
	specification := api.OpenAPI()
	if len(specification.Paths) == 0 {
		// Every deployment registers operations. None at all means the pass did
		// not reach them, and writing that over a good document would replace a
		// working generator input with an empty one.
		return nil, fmt.Errorf("%w: the composed application registered no operations",
			ErrOpenAPIExportUnavailable)
	}

	if format == OpenAPIFormatYAML {
		document, err = specification.YAML()
	} else {
		document, err = json.MarshalIndent(specification, "", "  ")
	}
	if err != nil {
		return nil, fmt.Errorf("serialize openapi document as %s: %w", format, err)
	}
	// A trailing newline, because this document is written to a file that people
	// and tools read line by line.
	return append(document, '\n'), nil
}

// normalizeOpenAPIFormat resolves the requested format, defaulting to JSON.
// An unrecognized name is refused rather than silently treated as the default:
// a misspelled format that quietly produced JSON would be discovered by whatever
// consumed the file, far away from the command that chose it.
func normalizeOpenAPIFormat(format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "":
		return OpenAPIFormatJSON, nil
	case OpenAPIFormatJSON:
		return OpenAPIFormatJSON, nil
	case OpenAPIFormatYAML, "yml":
		return OpenAPIFormatYAML, nil
	default:
		return "", fmt.Errorf("openapi format %q is not one of %s, %s",
			format, OpenAPIFormatJSON, OpenAPIFormatYAML)
	}
}
