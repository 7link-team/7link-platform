package todo

import (
	"context"
	"fmt"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	todoi18n "7link.com/platform/internal/todo/i18n"
	"7link.com/platform/pkg/appenv"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

// Module is the task module. It owns the todos table and exposes the task API.
type Module struct {
	runtime *appenv.Runtime
	ids     guid.Source
	service *service
}

func NewModule(rt *appenv.Runtime) *Module { return &Module{runtime: rt} }

func (*Module) ModuleName() string { return Name }

// ModuleDependencies: i18n supplies the message catalog, guid supplies task
// identifiers.
func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name}
}

// HTTPMiddleware makes the raw query string reachable from the list handler,
// which parses its filters from the module's query contract rather than from
// declared struct fields.
func (*Module) HTTPMiddleware(next http.Handler) http.Handler {
	return RawQueryMiddleware(next)
}

var (
	_ module.HTTPMiddlewareModule    = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.HealthChecker           = (*Module)(nil)
	_ module.HTTPRegistrar           = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)

func (m *Module) InjectServices(services *module.Services) error {
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return err
	}
	m.ids = ids
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := todoi18n.Register(); err != nil {
		return fmt.Errorf("register i18n: %w", err)
	}
	// Fail here rather than at the first import: a header that resolves to two
	// columns in some locale would silently load values into the wrong field.
	if _, err := buildHeaderIndex(); err != nil {
		return err
	}
	m.service = newService(m.runtime, m.ids)
	return nil
}

// CheckHealth reports the module unhealthy when it cannot reach a writable
// datasource, since every task operation needs one.
func (m *Module) CheckHealth(context.Context) error {
	if m.service == nil || m.service.writer() == nil {
		return ErrUnavailable
	}
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m.service == nil {
		return fmt.Errorf("register todo: service is unavailable")
	}
	// The filter parameters are documented from the same declaration that parses
	// them: they are read from the raw query rather than bound to struct fields,
	// so nothing else would put them in the generated document.
	//
	// Building that declaration needs the generated column set, which needs a
	// datasource. Without one every task route already answers "unavailable", so
	// the routes are still registered — there is simply nothing to document the
	// filters from, and failing registration would turn a dead module into a
	// dead server.
	var filterParams []*huma.Param
	if contract, err := m.service.contract(); err == nil {
		filterParams = contract.Parameters()
	}
	// The transfer routes are registered first: their paths are static where the
	// task routes take a parameter, and a static segment must not be captured as
	// an identifier.
	registerTransferHTTP(api, m.service, filterParams)
	registerHTTP(api, m.service, filterParams)
	return nil
}
