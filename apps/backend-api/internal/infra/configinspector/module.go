// Package configinspector provides a read-only effective configuration module.
package configinspector

import (
	"context"
	"fmt"
	"net/http"

	configx "7link.com/platform/pkg/config"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
)

const (
	Name = "config-inspector"
	Path = "/debug/config"
)

// Config controls the config inspector. Enabled is inherited from module.Config
// and read by the lifecycle's Enabler.
type Config struct {
	module.Config
}

type Module struct {
	value    any
	cfg      Config
	snapshot map[string]any
}

var (
	_ module.Module        = (*Module)(nil)
	_ module.Enabler       = (*Module)(nil)
	_ module.Initializer   = (*Module)(nil)
	_ module.HTTPRegistrar = (*Module)(nil)
)

// NewModule builds the config inspector. The snapshot is deferred to Initialize
// so construction never fails; an unsupported configuration still fails startup,
// just in the Initialize phase.
func NewModule(value any, cfg Config) *Module {
	return &Module{value: value, cfg: cfg}
}

func (m *Module) Enabled() bool { return m.cfg.Enabled }

func (m *Module) Initialize(context.Context) error {
	snapshot, err := configx.Inspect(m.value)
	if err != nil {
		return fmt.Errorf("create config inspector: %w", err)
	}
	m.snapshot = snapshot
	return nil
}

func (*Module) ModuleName() string { return Name }

func (m *Module) RegisterHTTP(_ context.Context, router chi.Router, api huma.API) error {
	router.Get(Path, m.handle)
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "get-effective-configuration",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "Inspect effective configuration",
		Description: "Returns the effective application configuration with sensitive values redacted.",
		Tags:        []string{"Diagnostics"},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "Effective configuration",
				Content: map[string]*huma.MediaType{
					"application/json": {Schema: &huma.Schema{Type: huma.TypeObject}},
				},
			},
		},
	})
	return nil
}

func (m *Module) handle(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	respx.WriteOK(writer, request, m.snapshot)
}
