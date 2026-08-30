// Package pprof provides runtime profiling endpoints and an embedded analysis UI.
package pprof

import (
	"context"
	"net/http"
	stdpprof "net/http/pprof"

	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
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
	name string
	cfg  Config
}

var (
	_ module.Module        = (*Module)(nil)
	_ module.Enabler       = (*Module)(nil)
	_ module.HTTPRegistrar = (*Module)(nil)
)

// NewModule builds the pprof module. The application name (UI title) is taken
// from the shared runtime.
func NewModule(rt module.Runtime, cfg Config) *Module {
	return &Module{name: rt.Name, cfg: cfg}
}

func (m *Module) Enabled() bool { return m.cfg.Enabled }

func (*Module) ModuleName() string { return Name }

func (m *Module) RegisterHTTP(_ context.Context, router chi.Router, api huma.API) error {
	ui := newPprofUI(m.name)
	router.Get(Path, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, Path+"/", http.StatusMovedPermanently)
	})
	router.Get(Path+"/", stdpprof.Index)
	router.Get(Path+"/cmdline", stdpprof.Cmdline)
	router.Get(Path+"/profile", stdpprof.Profile)
	router.Get(Path+"/symbol", stdpprof.Symbol)
	router.Get(Path+"/trace", stdpprof.Trace)
	router.Get(UIPath, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, UIPath+"/", http.StatusMovedPermanently)
	})
	router.Handle(UIPath+"/*", http.StripPrefix(UIPath, ui))
	router.Get(Path+"/*", stdpprof.Index)

	registerPprofOpenAPI(api)
	return nil
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
		Tags:        []string{"Diagnostics"},
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
