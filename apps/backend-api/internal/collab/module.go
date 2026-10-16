// Package collab owns project communication channels.
package collab

//go:generate go run ./dbgen

import (
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"context"
	"time"
)

const Name = "collab"

type Config struct{ module.Config }
type Module struct {
	audit      audit.Appender
	config     Config
	names      query.TableNames
	ids        guid.Source
	members    core.ActiveMemberChecker
	agents     fleet.AgentDirectory
	principals identity.PrincipalStateStore
	now        func() time.Time
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	return &Module{config: config, names: query.BindNames(rt.TablePrefix()), now: func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{guid.Name, core.Name, identity.Name, fleet.Name, audit.Name}
}
func (m *Module) ProvideServices(s *module.Services) error {
	return module.Provide[ChannelProvisioner](s, ChannelProvisioner{CreateDefaultOn: m.CreateDefaultOn, RenameDefaultOn: m.RenameDefaultOn, ArchiveProjectChannelsOn: m.ArchiveProjectChannelsOn})
}
func (m *Module) InjectServices(s *module.Services) error {
	var err error
	if m.agents, err = module.Resolve[fleet.AgentDirectory](s); err != nil {
		return err
	}
	if m.ids, err = module.Resolve[guid.Source](s); err != nil {
		return err
	}
	if m.members, err = module.Resolve[core.ActiveMemberChecker](s); err != nil {
		return err
	}
	if m.principals, err = module.Resolve[identity.PrincipalStateStore](s); err != nil {
		return err
	}
	m.audit, err = module.Resolve[audit.Appender](s)
	return err
}
func (m *Module) Initialize(context.Context) error {
	if m.audit == nil || m.ids == nil || m.members == nil || m.principals.LockActiveRow == nil || m.principals.HumanByLogin == nil || m.agents.ByPrincipalOn == nil {
		return ErrUnavailable
	}
	return nil
}

var (
	_ module.Enabler                 = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)
