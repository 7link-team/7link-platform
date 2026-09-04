// Package migrationstatus provides a read-only database migration status module.
package migrationstatus

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

const (
	Name       = "migration-status"
	Path       = "/migration-status"
	LegacyPath = "/debug/migrations"
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

// Config controls the migration-status module. Enabled is inherited from
// module.Config and read by the lifecycle's Enabler.
type Config struct {
	module.Config
}

type Module struct {
	runtime *appenv.Runtime
	cfg     Config
	inspect Inspector
}

var (
	_ module.Module               = (*Module)(nil)
	_ module.Enabler              = (*Module)(nil)
	_ module.ServiceInjector      = (*Module)(nil)
	_ module.HTTPRegistrar        = (*Module)(nil)
	_ module.RawHTTPHandlerModule = (*Module)(nil)
)

func NewModule(rt *appenv.Runtime, cfg Config) *Module {
	return &Module{runtime: rt, cfg: cfg}
}

func (m *Module) Enabled() bool { return m.cfg.Enabled }

// InjectServices resolves the migration inspector provided by the app runtime.
func (m *Module) InjectServices(services *module.Services) error {
	inspect, err := module.Resolve[Inspector](services)
	if err != nil {
		return err
	}
	m.inspect = inspect
	return nil
}

func (*Module) ModuleName() string { return Name }

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m.inspect == nil {
		return fmt.Errorf("register migration status: inspector is unavailable")
	}
	for _, operation := range []*huma.Operation{
		migrationStatusOperation(Path, "get-migration-status", false),
		migrationStatusOperation(LegacyPath, "get-legacy-migration-status", true),
	} {
		api.OpenAPI().AddOperation(operation)
	}
	return nil
}

// RawHTTPRoutes contributes the migration-status handler, mounted on the
// server's router.
func (m *Module) RawHTTPRoutes() []module.RawRoute {
	handler := http.HandlerFunc(m.handle)
	return []module.RawRoute{
		{Method: http.MethodGet, Path: Path, Handler: handler},
		{Method: http.MethodGet, Path: LegacyPath, Handler: handler},
	}
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

func migrationStatusOperation(path, operationID string, deprecated bool) *huma.Operation {
	return &huma.Operation{
		OperationID: operationID,
		Method:      http.MethodGet,
		Path:        path,
		Summary:     "Inspect database migrations",
		Description: "Compares embedded and applied migration versions for every registered module.",
		Tags:        []string{"Diagnostics"},
		Deprecated:  deprecated,
		Responses: map[string]*huma.Response{
			"200": jsonResponse("Migration status"),
			"503": jsonResponse("Migration status unavailable"),
		},
	}
}

func jsonResponse(description string) *huma.Response {
	return &huma.Response{
		Description: description,
		Content: map[string]*huma.MediaType{
			"application/json": {Schema: &huma.Schema{Type: huma.TypeObject}},
		},
	}
}
