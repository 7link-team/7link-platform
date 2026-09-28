// Package configinspector provides a read-only effective configuration module.
package configinspector

import (
	"context"
	"fmt"
	"net/http"

	"7link.com/platform/pkg/appenv"
	configx "7link.com/platform/pkg/config"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
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
	runtime  *appenv.Runtime
	value    any
	cfg      Config
	snapshot map[string]any
}

var (
	_ module.Module               = (*Module)(nil)
	_ module.Enabler              = (*Module)(nil)
	_ module.Initializer          = (*Module)(nil)
	_ module.HTTPRegistrar        = (*Module)(nil)
	_ module.RawHTTPHandlerModule = (*Module)(nil)
)

// NewModule builds the config inspector. The snapshot is deferred to Initialize
// so construction never fails; an unsupported configuration still fails startup,
// just in the Initialize phase.
func NewModule(rt *appenv.Runtime, value any, cfg Config) *Module {
	return &Module{runtime: rt, value: value, cfg: cfg}
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

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "get-effective-configuration",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "Inspect effective configuration",
		Description: "Returns the effective application configuration with sensitive values redacted.",
		Tags:        []string{"system"},
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

// RawHTTPRoutes contributes the config inspector handler, mounted on the
// server's router.
func (m *Module) RawHTTPRoutes() []module.RawRoute {
	return []module.RawRoute{{Method: http.MethodGet, Path: Path, Handler: http.HandlerFunc(m.handle)}}
}

func (m *Module) handle(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	respx.WriteOK(writer, request, m.snapshot)
}
