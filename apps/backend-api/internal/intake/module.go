// Package intake owns external requirement sources and confirmations.
package intake

//go:generate go run ./dbgen

import (
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
)

const Name = "intake"

type Config struct{ module.Config }

// Module owns intake persistence and reference capabilities.
type Module struct {
	config Config
	names  query.TableNames
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	return &Module{config: config, names: query.BindNames(rt.TablePrefix())}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (m *Module) ProvideServices(s *module.Services) error {
	return module.Provide[IntakeReader](s, IntakeReader{ContactsOn: m.ContactsOn, GetOn: m.GetOn})
}

var (
	_ module.Enabler                 = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)
