// Package humax builds the application's configured Huma API.
package humax

import (
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"7link.com/platform/pkg/extension/huma/docs"
	"7link.com/platform/pkg/extension/huma/respx"
)

const OpenAPIPath = "/openapi"

// New installs the shared router middleware, response contract, docs, and
// OpenAPI configuration before any module registers operations.
func New(router chi.Router, name, version string) huma.API {
	name = strings.TrimSpace(name)
	router.Use(respx.Timing)
	router.Use(respx.Locale)

	config := huma.DefaultConfig(name+" API", version)
	// DefaultConfig injects a top-level $schema property into response bodies.
	// The application contract is the exact {code,message,meta,data} envelope.
	config.CreateHooks = nil
	config.OpenAPIPath = OpenAPIPath
	config.Transformers = append(config.Transformers, respx.LocalizeMessage)
	config = docs.Register(router, config, name)

	respx.Install()
	return humachi.New(router, config)
}
