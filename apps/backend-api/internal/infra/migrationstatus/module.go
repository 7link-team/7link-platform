// Package migrationstatus provides a read-only database migration status module.
package migrationstatus

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
)

const (
	Name = "migration-status"
	Path = "/debug/migrations"
)

type Report struct {
	Module string        `json:"module"`
	Status goosex.Status `json:"status"`
}

type Inspector func(context.Context) ([]Report, error)

type response struct {
	State   goosex.State `json:"state"`
	Modules []Report     `json:"modules"`
}

type Module struct {
	inspect Inspector
}

var (
	_ module.Module        = (*Module)(nil)
	_ module.HTTPRegistrar = (*Module)(nil)
)

func NewModule(inspect Inspector) *Module {
	return &Module{inspect: inspect}
}

func (*Module) ModuleName() string { return Name }

func (m *Module) RegisterHTTP(_ context.Context, router chi.Router, api huma.API) error {
	if m.inspect == nil {
		return fmt.Errorf("register migration status: inspector is unavailable")
	}
	router.Get(Path, m.handle)
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "get-migration-status",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "Inspect database migrations",
		Description: "Compares embedded and applied migration versions for every registered module.",
		Tags:        []string{"Operations"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse("Migration status"),
			"503": jsonResponse("Migration status unavailable"),
		},
	})
	return nil
}

func (m *Module) handle(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	reports, err := m.inspect(request.Context())
	if err != nil {
		slog.Error("inspect migration status", "error", err)
		respx.WriteError(writer, request, http.StatusServiceUnavailable, "respx.internal_server_error", 0)
		return
	}
	respx.WriteOK(writer, request, response{State: overallState(reports), Modules: reports})
}

func overallState(reports []Report) goosex.State {
	state := goosex.StateClean
	priority := map[goosex.State]int{
		goosex.StateClean: 0, goosex.StatePending: 1, goosex.StateAhead: 2, goosex.StateDiverged: 3,
	}
	for _, report := range reports {
		if priority[report.Status.State] > priority[state] {
			state = report.Status.State
		}
	}
	return state
}

func jsonResponse(description string) *huma.Response {
	return &huma.Response{
		Description: description,
		Content: map[string]*huma.MediaType{
			"application/json": {Schema: &huma.Schema{Type: huma.TypeObject}},
		},
	}
}
