// Package collab owns project communication channels.
package collab

//go:generate go run ./dbgen

import (
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"time"
)

const Name = "collab"

type Config struct {
	module.Config
	ConnectorEncryptionKey              string
	ConnectorEncryptionKeyFile          string
	PreviousConnectorEncryptionKeys     []string
	PreviousConnectorEncryptionKeyFiles []string
}
type Module struct {
	channelDispatch      ChannelDispatchProposer
	channelDispatchFacts ChannelDispatchFacts
	runtime              *appenv.Runtime
	policy               core.EntityConstraintOn
	tenantPolicy         core.TenantConstraintOn
	owner                core.EntityOwnerResolver
	registrar            *authz.Registrar
	publisher            events.Publisher
	replay               idempotency.Store
	audit                audit.Appender
	config               Config
	names                query.TableNames
	ids                  guid.Source
	members              core.ActiveMemberChecker
	agents               fleet.AgentDirectory
	principals           identity.PrincipalStateStore
	now                  func() time.Time
	messageObserver      MessageObserver
	workCreator          MessageWorkCreator
	workReplay           MessageWorkReplay
	fileResources        files.ConversationResources
	localesRegistered    bool
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	return &Module{runtime: rt, config: config, names: query.BindNames(rt.TablePrefix()), now: func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{jobs.Name, files.Name, guid.Name, core.Name, identity.Name, fleet.Name, audit.Name, events.Name, idempotency.Name, platformi18n.Name}
}
func (m *Module) ProvideServices(s *module.Services) error {
	if err := m.provideDisposition(s); err != nil {
		return err
	}
	if err := m.provideChannelDispatch(s); err != nil {
		return err
	}
	if err := m.provideSystemCards(s); err != nil {
		return err
	}
	if err := m.provideEditedMentions(s); err != nil {
		return err
	}
	if err := m.provideIntakeConversations(s); err != nil {
		return err
	}
	if err := module.Provide[RealtimeTopics](s, RealtimeTopics{AuthorizeChannel: m.authorizeRealtimeChannel, AuthorizeThread: m.authorizeRealtimeThread}); err != nil {
		return err
	}
	if err := m.provideDirectConversations(s); err != nil {
		return err
	}
	if err := m.provideNotificationMentions(s); err != nil {
		return err
	}
	if err := m.provideLoopAnchor(s); err != nil {
		return err
	}
	if err := m.provideWorkConnector(s); err != nil {
		return err
	}
	if err := module.Provide[MessageObserverConnector](s, MessageObserverConnector{Bind: m.bindMessageObserver}); err != nil {
		return err
	}
	if err := module.Provide[ThreadAccess](s, ThreadAccess{CheckOn: m.threadAccessOn}); err != nil {
		return err
	}
	return module.Provide[ChannelProvisioner](s, ChannelProvisioner{CreateDefaultOn: m.CreateDefaultOn, RenameDefaultOn: m.RenameDefaultOn, ArchiveProjectChannelsOn: m.ArchiveProjectChannelsOn})
}
func (m *Module) InjectServices(s *module.Services) error {
	if err := m.injectRetirement(s); err != nil {
		return err
	}
	if err := m.injectAttachments(s); err != nil {
		return err
	}
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
	if m.audit, err = module.Resolve[audit.Appender](s); err != nil {
		return err
	}
	return m.injectConversations(s)
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
