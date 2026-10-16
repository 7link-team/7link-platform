// Package fleet owns Agent identities and runtime infrastructure.
package fleet

//go:generate go run ./dbgen

import (
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
)

const Name = "fleet"

type Config struct{ module.Config }

// Module owns fleet records and exposes identity facts to business consumers.
type Module struct {
	config Config
	names  query.TableNames
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	return &Module{config: config, names: query.BindNames(rt.TablePrefix())}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (m *Module) ProvideServices(s *module.Services) error {
	return module.Provide[AgentDirectory](s, AgentDirectory{ByPrincipalOn: m.ByPrincipalOn})
}

func (*Module) ModuleName() string { return Name }

var (
	_ module.MigrationSourceProvider = (*Module)(nil)
	_ module.Enabler                 = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
)
