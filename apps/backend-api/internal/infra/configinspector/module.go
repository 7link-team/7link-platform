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

type Module struct {
	snapshot map[string]any
}

var (
	_ module.Module        = (*Module)(nil)
	_ module.HTTPRegistrar = (*Module)(nil)
)

func NewModule(value any) (*Module, error) {
	snapshot, err := configx.Inspect(value)
	if err != nil {
		return nil, fmt.Errorf("create config inspector: %w", err)
	}
	return &Module{snapshot: snapshot}, nil
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
