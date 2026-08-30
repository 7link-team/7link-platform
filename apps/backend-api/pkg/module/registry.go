package module

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	grpcx "7link.com/platform/pkg/extension/grpc"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

// phase is the Registry's lifecycle state.
type phase int

const (
	phaseNew phase = iota
	phaseRegistered
	phaseActivated
	phaseWired
	phaseInitialized
	phaseStarted
)

type entry struct {
	module Module
}

// Registry owns the lifecycle of the application's modules. Modules are
// registered once, then activated, wired, initialized, started, and stopped by
// the registry. Each module is constructed exactly once (by the caller); the
// registry manages state, ordering, and rollback uniformly.
type Registry struct {
	entries []*entry
	// services is the application-scoped service registry, sealed by Wire.
	services *Services
	// initialized is the rollback list: every Deinitializer module (pre-seeded
	// at Register, since such modules own resources from construction) plus
	// every module that completed Initialize. Deinitialize consumes it in
	// reverse order.
	initialized []*entry
	// started contains only modules that completed Start; shutdown stops them
	// in reverse order.
	started []*entry
	phase   phase
}

func NewRegistry() *Registry {
	return &Registry{}
}

// Register appends modules. Single-module invariants (nil, empty name, duplicate
// name) are validated for the whole batch before any module is appended, so a
// failing batch cannot leave a half-registered set. Every Deinitializer module
// is pre-seeded into the rollback list so resources owned from construction are
// released on any failure path, including failures before Wire.
func (r *Registry) Register(mods ...Module) error {
	if r.phase > phaseRegistered {
		return fmt.Errorf("module: register after activation is not allowed")
	}
	existing := make(map[string]bool, len(r.entries))
	for _, e := range r.entries {
		existing[e.module.ModuleName()] = true
	}
	batch := make([]*entry, 0, len(mods))
	for _, m := range mods {
		if isNilModule(m) {
			return fmt.Errorf("module: registered module is nil")
		}
		name := m.ModuleName()
		if name == "" {
			return fmt.Errorf("%T: module name is empty", m)
		}
		if existing[name] {
			return fmt.Errorf("module: duplicate module name %q", name)
		}
		existing[name] = true
		batch = append(batch, &entry{module: m})
	}
	for _, e := range batch {
		r.entries = append(r.entries, e)
		if _, cleanup := e.module.(Deinitializer); cleanup {
			r.initialized = append(r.initialized, e)
		}
	}
	if r.phase == phaseNew {
		r.phase = phaseRegistered
	}
	return nil
}

// Activate drops modules whose Enabler.Enabled is false, then re-validates
// Depender existence and ordering against the active set. A dependency that was
// disabled alongside its dependent is fine; a dependency that was disabled while
// its dependent stayed active is an error.
func (r *Registry) Activate() error {
	if r.phase != phaseRegistered {
		return fmt.Errorf("module: activate requires registered modules")
	}
	active := r.entries[:0]
	for _, e := range r.entries {
		if enabler, ok := e.module.(Enabler); ok && !enabler.Enabled() {
			continue
		}
		active = append(active, e)
	}
	r.entries = active

	indices := make(map[string]int, len(r.entries))
	for index, e := range r.entries {
		indices[e.module.ModuleName()] = index
	}
	for index, e := range r.entries {
		depender, ok := e.module.(Depender)
		if !ok {
			continue
		}
		for _, dependency := range depender.ModuleDependencies() {
			dependencyIndex, exists := indices[dependency]
			if !exists {
				return fmt.Errorf("module %q requires unregistered module %q", e.module.ModuleName(), dependency)
			}
			if dependencyIndex >= index {
				return fmt.Errorf("module %q must be registered after dependency %q", e.module.ModuleName(), dependency)
			}
		}
	}
	r.phase = phaseActivated
	return nil
}

// Wire runs the service wiring phase: module ServiceProviders first, then the
// app's cross-cutting providers (the provide callback), then Seal, then every
// ServiceInjector. Providers therefore always resolve before injectors run,
// independent of registration order.
func (r *Registry) Wire(provide func(*Services) error) error {
	if r.phase != phaseActivated {
		return fmt.Errorf("module: wire requires activated modules")
	}
	r.services = NewServices()
	for _, e := range r.entries {
		provider, ok := e.module.(ServiceProvider)
		if !ok {
			continue
		}
		if err := provider.ProvideServices(r.services); err != nil {
			return fmt.Errorf("%T provide services: %w", e.module, err)
		}
	}
	if provide != nil {
		if err := provide(r.services); err != nil {
			return err
		}
	}
	if err := r.services.Seal(); err != nil {
		return err
	}
	for _, e := range r.entries {
		injector, ok := e.module.(ServiceInjector)
		if !ok {
			continue
		}
		if err := injector.InjectServices(r.services); err != nil {
			return fmt.Errorf("%T inject services: %w", e.module, err)
		}
	}
	r.phase = phaseWired
	return nil
}

// Initialize runs the Initialize phase, stopping at the first failure so the app
// never starts against a half-initialized state.
func (r *Registry) Initialize(ctx context.Context) error {
	if r.phase != phaseWired {
		return fmt.Errorf("module: initialize requires wired modules")
	}
	for _, e := range r.entries {
		initializer, ok := e.module.(Initializer)
		if !ok {
			continue
		}
		if err := initializer.Initialize(ctx); err != nil {
			return fmt.Errorf("%T initialize: %w", e.module, err)
		}
		// A Deinitializer module was pre-seeded at Register; do not append twice.
		if !r.rollbackContains(e) {
			r.initialized = append(r.initialized, e)
		}
	}
	r.phase = phaseInitialized
	return nil
}

// Start runs the Start phase, stopping at the first failure.
func (r *Registry) Start(ctx context.Context) error {
	if r.phase != phaseInitialized {
		return fmt.Errorf("module: start requires initialized modules")
	}
	for _, e := range r.entries {
		starter, ok := e.module.(Starter)
		if !ok {
			continue
		}
		if err := starter.Start(ctx); err != nil {
			return fmt.Errorf("%T start: %w", e.module, err)
		}
		r.started = append(r.started, e)
	}
	r.phase = phaseStarted
	return nil
}

// RegisterHTTP runs the HTTP registration phase. It requires Started so modules
// are operational before routes are mounted.
func (r *Registry) RegisterHTTP(ctx context.Context, router chi.Router, api huma.API) error {
	if r.phase < phaseStarted {
		return fmt.Errorf("module: register http requires started modules")
	}
	for _, e := range r.entries {
		registrar, ok := e.module.(HTTPRegistrar)
		if !ok {
			continue
		}
		if err := registrar.RegisterHTTP(ctx, router, api); err != nil {
			return fmt.Errorf("%T register HTTP: %w", e.module, err)
		}
	}
	return nil
}

// RegisterGRPC runs the gRPC registration phase.
func (r *Registry) RegisterGRPC(registrar grpc.ServiceRegistrar) error {
	if r.phase < phaseStarted {
		return fmt.Errorf("module: register grpc requires started modules")
	}
	for _, e := range r.entries {
		registrarModule, ok := e.module.(GRPCRegistrar)
		if !ok {
			continue
		}
		registrarModule.RegisterGRPC(registrar)
	}
	return nil
}

// HTTPMiddlewares returns the server-wide middlewares in registration order.
func (r *Registry) HTTPMiddlewares() []func(http.Handler) http.Handler {
	middlewares := make([]func(http.Handler) http.Handler, 0)
	for _, e := range r.entries {
		middlewareModule, ok := e.module.(HTTPMiddlewareModule)
		if !ok {
			continue
		}
		middlewares = append(middlewares, middlewareModule.HTTPMiddleware)
	}
	return middlewares
}

// GRPCProtocolOptions assembles the gRPC server protocol options from the module
// registry. Protocol modules contribute their stats handlers and interceptors in
// registration order (earlier module = outer interceptor); the platform
// interceptors (locale, recovery, validation) always run innermost.
func (r *Registry) GRPCProtocolOptions() []grpc.ServerOption {
	var statsHandlers []stats.Handler
	var unary []grpc.UnaryServerInterceptor
	var stream []grpc.StreamServerInterceptor
	for _, e := range r.entries {
		if statsModule, ok := e.module.(StatsHandlerModule); ok {
			statsHandlers = append(statsHandlers, statsModule.GRPCServerStatsHandler())
		}
		if interceptor, ok := e.module.(UnaryInterceptorModule); ok {
			unary = append(unary, interceptor.UnaryServerInterceptor)
		}
		if interceptor, ok := e.module.(StreamInterceptorModule); ok {
			stream = append(stream, interceptor.StreamServerInterceptor)
		}
	}
	unary = append(unary,
		grpcx.LocaleUnaryInterceptor,
		grpcx.RecoveryUnaryInterceptor,
		grpcx.ValidationUnaryInterceptor,
	)
	stream = append(stream,
		grpcx.LocaleStreamInterceptor,
		grpcx.RecoveryStreamInterceptor,
		grpcx.ValidationStreamInterceptor,
	)

	options := make([]grpc.ServerOption, 0, len(statsHandlers)+2)
	for _, handler := range statsHandlers {
		options = append(options, grpc.StatsHandler(handler))
	}
	options = append(options,
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
	)
	return options
}

// RawHTTPRoutes collects the raw HTTP routes contributed by modules.
func (r *Registry) RawHTTPRoutes() []RawRoute {
	routes := make([]RawRoute, 0)
	for _, e := range r.entries {
		rawModule, ok := e.module.(RawHTTPHandlerModule)
		if !ok {
			continue
		}
		routes = append(routes, rawModule.RawHTTPRoutes()...)
	}
	return routes
}

// Quiesce disables new work in reverse start order while protocol listeners
// remain available for readiness checks during the drain delay.
func (r *Registry) Quiesce(ctx context.Context) error {
	var errs []error
	for i := len(r.started) - 1; i >= 0; i-- {
		e := r.started[i]
		if quiescer, ok := e.module.(Quiescer); ok {
			if err := quiescer.Quiesce(ctx); err != nil {
				errs = append(errs, fmt.Errorf("%T quiesce: %w", e.module, err))
			}
		}
	}
	return errors.Join(errs...)
}

// Stop runs the Stop phase in reverse start order, joining every error so one
// failing module cannot strand the rest. It clears the started list so a second
// shutdown is idempotent.
func (r *Registry) Stop(ctx context.Context) error {
	var errs []error
	for i := len(r.started) - 1; i >= 0; i-- {
		e := r.started[i]
		if stopper, ok := e.module.(Stopper); ok {
			if err := stopper.Stop(ctx); err != nil {
				errs = append(errs, fmt.Errorf("%T stop: %w", e.module, err))
			}
		}
	}
	r.started = nil
	return errors.Join(errs...)
}

// Deinitialize runs the Deinitialize phase in reverse rollback order, joining
// every error. It clears the rollback list so a second shutdown is idempotent.
// Deinitialize is safe to call from any failed state: every Deinitializer module
// was pre-seeded at Register, and its Deinitialize must be nil-safe and
// idempotent (it may run before Initialize).
func (r *Registry) Deinitialize(ctx context.Context) error {
	var errs []error
	for i := len(r.initialized) - 1; i >= 0; i-- {
		e := r.initialized[i]
		if deinitializer, ok := e.module.(Deinitializer); ok {
			if err := deinitializer.Deinitialize(ctx); err != nil {
				errs = append(errs, fmt.Errorf("%T deinitialize: %w", e.module, err))
			}
		}
	}
	r.initialized = nil
	return errors.Join(errs...)
}

// Services returns the sealed service registry (nil before Wire).
func (r *Registry) Services() *Services {
	return r.services
}

// Modules returns the active module set in registration order as a defensive copy.
func (r *Registry) Modules() []Module {
	mods := make([]Module, 0, len(r.entries))
	for _, e := range r.entries {
		mods = append(mods, e.module)
	}
	return mods
}

// ByName returns the active module with the given name.
func (r *Registry) ByName(name string) (Module, bool) {
	for _, e := range r.entries {
		if e.module.ModuleName() == name {
			return e.module, true
		}
	}
	return nil, false
}

func (r *Registry) rollbackContains(target *entry) bool {
	for _, e := range r.initialized {
		if e == target {
			return true
		}
	}
	return false
}

// isNilModule reports whether a module interface holds a nil value, including
// typed-nil values (nil pointers, interfaces, funcs, maps, slices, channels).
func isNilModule(value Module) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	}
	return false
}
