// Package intake owns external requirement sources and confirmations.
package intake

//go:generate go run ./dbgen

import (
	intakei18n "7link.com/platform/internal/intake/i18n"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"context"
)

const Name = "intake"

type Config struct{ module.Config }

// Module owns intake persistence and reference capabilities.
type Module struct {
	triageRequestAuthority   TriageRequestAuthority
	triageRequestCoordinator TriageRequestCoordinator
	delegatedNewAuthority    DelegatedNewAuthority
	triageAgent              TriageAgentReader
	dispositionRoundCloser   DispositionRoundCloser
	dispositionNotification  DispositionNotification
	mergeAuthority           DelegatedNewAuthority
	channelTiming            ChannelTimingReader
	brief                    signedBriefServices
	config                   Config
	names                    query.TableNames
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	return &Module{config: config, names: query.BindNames(rt.TablePrefix())}
}
func (*Module) ModuleName() string               { return Name }
func (m *Module) Enabled() bool                  { return m != nil && m.config.Enabled }
func (*Module) Initialize(context.Context) error { return intakei18n.Register() }
func (m *Module) ProvideServices(s *module.Services) error {
	if err := m.provideSubmissionEvents(s); err != nil {
		return err
	}
	if err := m.provideMergedReplies(s); err != nil {
		return err
	}
	if err := m.provideMemorySourceText(s); err != nil {
		return err
	}
	if err := m.provideDelegatedNew(s); err != nil {
		return err
	}
	if err := m.provideTriageAgent(s); err != nil {
		return err
	}
	if err := m.provideDispositionRound(s); err != nil {
		return err
	}
	if err := m.provideChannelTiming(s); err != nil {
		return err
	}
	if e := m.provideNotificationFacts(s); e != nil {
		return e
	}
	if e := m.provideApiReceiving(s); e != nil {
		return e
	}
	if e := m.provideChangeSources(s); e != nil {
		return e
	}
	if e := module.Provide[SignedBriefs](s, SignedBriefs{SignOn: m.signBriefOn, ReadOn: m.readSignedBriefOn}); e != nil {
		return e
	}
	return module.Provide[IntakeReader](s, IntakeReader{ContactsOn: m.ContactsOn, GetOn: m.GetOn, ProjectFactsOn: m.ProjectFactsOn})
}

var (
	_ module.Initializer             = (*Module)(nil)
	_ module.Enabler                 = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)
