// Package appenv defines the application-lifetime environment shared with
// infrastructure modules.
package appenv

import (
	"errors"
	"sync"

	gormx "7link.com/platform/pkg/extension/gorm"
)

// Identity is the application's identity. It is distinct from a module's
// identity, which is reported exclusively by module.ModuleName.
type Identity struct {
	name    string
	version string
}

// NewIdentity creates an immutable application identity.
func NewIdentity(name, version string) Identity {
	return Identity{name: name, version: version}
}

// Name returns the application name.
func (i Identity) Name() string { return i.name }

// Version returns the application version.
func (i Identity) Version() string { return i.version }

// Runtime owns stable references to application-level infrastructure managers.
// The App creates one Runtime and keeps it for its entire lifetime; modules may
// retain the same pointer. Manager references cannot be replaced after
// construction, while each manager remains responsible for its own routing,
// health, reconnection, and other live state.
//
// Runtime is intentionally not a registry for module or business services.
// Those dependencies belong in module.Services as narrow interfaces.
type Runtime struct {
	identity    Identity
	database    *gormx.DatasourceRouter
	tablePrefix string

	closeOnce sync.Once
	closeErr  error
	closers   []func() error
}

// NewRuntime builds the application environment. New manager types should be
// added as constructor options once they have a real consumer; existing
// managers remain stable for the lifetime of the returned Runtime.
func NewRuntime(identity Identity, options ...Option) *Runtime {
	runtime := &Runtime{identity: identity}
	for _, option := range options {
		if option != nil {
			option(runtime)
		}
	}
	return runtime
}

// Option contributes an application-level infrastructure manager at the
// composition root.
type Option func(*Runtime)

// WithDatabase installs the application's datasource router.
func WithDatabase(database *gormx.DatasourceRouter) Option {
	return func(runtime *Runtime) {
		runtime.database = database
		if database != nil {
			runtime.closers = append(runtime.closers, database.Close)
		}
	}
}

// WithTablePrefix installs the prefix prepended to every table this
// application owns. It is an application-lifetime schema property, so it lives
// beside the router rather than being threaded through each module's config.
func WithTablePrefix(prefix string) Option {
	return func(runtime *Runtime) { runtime.tablePrefix = prefix }
}

// TablePrefix returns the prefix every persisting module must prepend to its
// table names. Empty means unprefixed.
func (r *Runtime) TablePrefix() string {
	return r.tablePrefix
}

// Identity returns an immutable copy of the application identity.
func (r *Runtime) Identity() Identity {
	return r.identity
}

// Database returns the stable datasource router. Callers resolve Read or Write
// on the router at operation time rather than retaining a selected connection.
func (r *Runtime) Database() *gormx.DatasourceRouter {
	return r.database
}

// Close releases application-level infrastructure managers in reverse
// construction order. It is safe to call more than once.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		var errs []error
		for index := len(r.closers) - 1; index >= 0; index-- {
			if err := r.closers[index](); err != nil {
				errs = append(errs, err)
			}
		}
		r.closeErr = errors.Join(errs...)
	})
	return r.closeErr
}
