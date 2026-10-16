// Package idempotency owns shared request deduplication records.
package idempotency

//go:generate go run ./dbgen
import (
	"7link.com/platform/internal/infra/guid"
	idemi18n "7link.com/platform/internal/infra/idempotency/i18n"
	"7link.com/platform/internal/infra/idempotency/query"
	"7link.com/platform/pkg/appenv"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"time"
)

const Name = "idempotency"

type Module struct {
	names query.TableNames
	ids   guid.Source
	now   func() time.Time
}

func NewModule(rt *appenv.Runtime) *Module {
	return &Module{names: query.BindNames(rt.TablePrefix()), now: func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }}
}
func (*Module) ModuleName() string                         { return Name }
func (*Module) ModuleDependencies() []string               { return []string{platformi18n.Name, guid.Name} }
func (m *Module) ProvideServices(s *module.Services) error { return module.Provide[Store](s, m) }
func (m *Module) InjectServices(s *module.Services) error {
	var err error
	m.ids, err = module.Resolve[guid.Source](s)
	return err
}
func (m *Module) Initialize(context.Context) error {
	if m.ids == nil {
		return ErrUnavailable
	}
	return idemi18n.Register()
}

var (
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
	_ Store                          = (*Module)(nil)
)
