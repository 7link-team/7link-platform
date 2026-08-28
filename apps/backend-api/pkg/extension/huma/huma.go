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

// Config controls the API metadata and HTTP resources owned by Huma.
type Config struct {
	Name    string
	Version string

	Landing LandingConfig
	Docs    EndpointConfig
	OpenAPI EndpointConfig
}

type LandingConfig struct {
	Enabled         bool
	AdvertiseHealth bool
}

// EndpointConfig independently controls route registration and landing-page
// advertisement. An endpoint is never advertised when it is disabled.
type EndpointConfig struct {
	Enabled   bool
	Advertise bool
}

func DefaultConfig(name, version string) Config {
	return Config{
		Name:    name,
		Version: version,
		Landing: LandingConfig{Enabled: true},
		Docs:    EndpointConfig{Enabled: true, Advertise: true},
		OpenAPI: EndpointConfig{Enabled: true, Advertise: true},
	}
}

// New installs the shared router middleware, response contract, docs, and
// OpenAPI configuration before any module registers operations.
func New(router chi.Router, settings Config) huma.API {
	name := strings.TrimSpace(settings.Name)
	router.Use(respx.Timing)
	router.Use(respx.Locale)

	config := huma.DefaultConfig(name+" API", settings.Version)
	// DefaultConfig injects a top-level $schema property into response bodies.
	// The application contract is the exact {code,message,meta,data} envelope.
	config.CreateHooks = nil
	if settings.OpenAPI.Enabled {
		config.OpenAPIPath = OpenAPIPath
	} else {
		config.OpenAPIPath = ""
	}
	config.Transformers = append(config.Transformers, respx.LocalizeMessage)
	config = docs.Register(router, config, docs.Options{
		Name:             name,
		LandingEnabled:   settings.Landing.Enabled,
		DocsEnabled:      settings.Docs.Enabled,
		AdvertiseDocs:    settings.Docs.Advertise,
		AdvertiseOpenAPI: settings.OpenAPI.Advertise,
		AdvertiseHealth:  settings.Landing.AdvertiseHealth,
	})

	respx.Install()
	return humachi.New(router, config)
}
