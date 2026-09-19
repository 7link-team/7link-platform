// Package pprof provides runtime profiling endpoints and an embedded analysis UI.
package pprof

import (
	"context"
	"net/http"
	stdpprof "net/http/pprof"

	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

const (
	Name   = "pprof"
	Path   = "/debug/pprof"
	UIPath = Path + "/ui"
)

// Config controls the pprof module. Enabled is inherited from module.Config and
// read by the lifecycle's Enabler.
type Config struct {
	module.Config
}

// Module owns the standard runtime profiling endpoints and embedded analysis UI.
type Module struct {
	runtime *appenv.Runtime
	cfg     Config
}

var (
	_ module.Module               = (*Module)(nil)
	_ module.Enabler              = (*Module)(nil)
	_ module.HTTPRegistrar        = (*Module)(nil)
	_ module.RawHTTPHandlerModule = (*Module)(nil)
)

// NewModule builds the pprof module. The application name (UI title) is taken
// from the shared runtime.
func NewModule(rt *appenv.Runtime, cfg Config) *Module {
	return &Module{runtime: rt, cfg: cfg}
}

func (m *Module) Enabled() bool { return m.cfg.Enabled }

func (*Module) ModuleName() string { return Name }

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	registerPprofOpenAPI(api)
	return nil
}

// RawHTTPRoutes contributes the runtime profiling handlers and the embedded
// analysis UI, mounted on the server's router.
func (m *Module) RawHTTPRoutes() []module.RawRoute {
	ui := newPprofUI(m.runtime.Identity().Name())
	return []module.RawRoute{
		{Method: http.MethodGet, Path: Path, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, Path+"/", http.StatusMovedPermanently)
		})},
		{Method: http.MethodGet, Path: Path + "/", Handler: http.HandlerFunc(stdpprof.Index)},
		{Method: http.MethodGet, Path: Path + "/cmdline", Handler: http.HandlerFunc(stdpprof.Cmdline)},
		{Method: http.MethodGet, Path: Path + "/profile", Handler: http.HandlerFunc(stdpprof.Profile)},
		{Method: http.MethodGet, Path: Path + "/symbol", Handler: http.HandlerFunc(stdpprof.Symbol)},
		{Method: http.MethodGet, Path: Path + "/trace", Handler: http.HandlerFunc(stdpprof.Trace)},
		{Method: http.MethodGet, Path: UIPath, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, UIPath+"/", http.StatusMovedPermanently)
		})},
		// The analysis UI also handles capture POSTs, so it is mounted for every
		// method (empty Method).
		{Method: "", Path: UIPath + "/*", Handler: http.StripPrefix(UIPath, ui)},
		{Method: http.MethodGet, Path: Path + "/*", Handler: http.HandlerFunc(stdpprof.Index)},
	}
}

func registerPprofOpenAPI(api huma.API) {
	for _, operation := range []*huma.Operation{
		pprofOperation(
			"get-pprof-index",
			Path+"/",
			"List runtime profiles",
			"Returns the standard Go runtime profiling index.",
			htmlResponse("Runtime profile index"),
		),
		pprofOperation(
			"get-pprof-cmdline",
			Path+"/cmdline",
			"Get process command line",
			"Returns the command line invocation with arguments separated by NUL bytes.",
			textResponse("Process command line"),
		),
		pprofOperation(
			"capture-pprof-cpu-profile",
			Path+"/profile",
			"Capture a CPU profile",
			"Captures a pprof-formatted CPU profile. The default duration is 30 seconds.",
			binaryResponse("CPU profile"),
			secondsParameter(huma.TypeInteger),
		),
		pprofOperation(
			"resolve-pprof-symbols",
			Path+"/symbol",
			"Resolve program counters",
			"Returns function names for program counters supplied in the raw query string.",
			textResponse("Resolved program counters"),
		),
		pprofOperation(
			"capture-pprof-trace",
			Path+"/trace",
			"Capture an execution trace",
			"Captures a Go execution trace. The default duration is 1 second.",
			binaryResponse("Execution trace"),
			secondsParameter(huma.TypeNumber),
		),
		pprofOperation(
			"get-pprof-runtime-profile",
			Path+"/{profile}",
			"Get a runtime profile",
			"Returns a named runtime profile such as heap, goroutine, allocs, block, mutex, or threadcreate.",
			profileResponse(),
			&huma.Param{
				Name:        "profile",
				In:          "path",
				Description: "Runtime profile name",
				Required:    true,
				Schema:      &huma.Schema{Type: huma.TypeString},
			},
			&huma.Param{
				Name:        "debug",
				In:          "query",
				Description: "Use a positive value to return a plaintext profile",
				Schema:      &huma.Schema{Type: huma.TypeInteger, Minimum: float64Pointer(0)},
			},
			&huma.Param{
				Name:        "gc",
				In:          "query",
				Description: "Run garbage collection before capturing a heap profile when positive",
				Schema:      &huma.Schema{Type: huma.TypeInteger, Minimum: float64Pointer(0)},
			},
			secondsParameter(huma.TypeInteger),
		),
		pprofOperation(
			"open-pprof-web-ui",
			UIPath+"/",
			"Open runtime profile analysis",
			"Opens the embedded Google pprof profile capture and analysis interface.",
			htmlResponse("Runtime profile analysis interface"),
		),
	} {
		api.OpenAPI().AddOperation(operation)
	}
}

func pprofOperation(
	operationID string,
	path string,
	summary string,
	description string,
	response *huma.Response,
	parameters ...*huma.Param,
) *huma.Operation {
	return &huma.Operation{
		OperationID: operationID,
		Method:      http.MethodGet,
		Path:        path,
		Summary:     summary,
		Description: description,
		Tags:        []string{"diagnostics"},
		Parameters:  parameters,
		Responses: map[string]*huma.Response{
			"200": response,
			"400": textResponse("Invalid profiling parameters"),
			"404": textResponse("Unknown runtime profile"),
			"405": textResponse("Method not allowed"),
			"500": textResponse("Runtime profiling failed"),
		},
	}
}

func secondsParameter(schemaType string) *huma.Param {
	return &huma.Param{
		Name:        "seconds",
		In:          "query",
		Description: "Profile capture duration in seconds",
		Schema:      &huma.Schema{Type: schemaType, ExclusiveMinimum: float64Pointer(0)},
	}
}

func htmlResponse(description string) *huma.Response {
	return contentResponse(description, "text/html", &huma.Schema{Type: huma.TypeString})
}

func textResponse(description string) *huma.Response {
	return contentResponse(description, "text/plain", &huma.Schema{Type: huma.TypeString})
}

func binaryResponse(description string) *huma.Response {
	return contentResponse(description, "application/octet-stream", &huma.Schema{
		Type:   huma.TypeString,
		Format: "binary",
	})
}

func profileResponse() *huma.Response {
	response := binaryResponse("Runtime profile")
	response.Content["text/plain"] = &huma.MediaType{Schema: &huma.Schema{Type: huma.TypeString}}
	return response
}

func contentResponse(description, contentType string, schema *huma.Schema) *huma.Response {
	return &huma.Response{
		Description: description,
		Content: map[string]*huma.MediaType{
			contentType: {Schema: schema},
		},
	}
}

func float64Pointer(value float64) *float64 {
	return &value
}
