// Package agents owns collaboration protocols; fleet retains runtime identity.
package agents

//go:generate go run ./dbgen

import (
	agentsi18n "7link.com/platform/internal/agents/i18n"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge"
	"context"
	"errors"
	"gorm.io/gorm"
	"net/http"
	"time"

	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
)

const Name = "agents"

type Config struct {
	module.Config
	EncryptionKey, EncryptionKeyFile                   string
	PreviousEncryptionKeys, PreviousEncryptionKeyFiles []string
}
type Module struct {
	retirementHandover    RetirementHandover
	retirementRules       knowledge.RetirementRules
	performance           performanceSources
	retirementRuntime     fleet.AgentRetirementRuntime
	retirementWork        delivery.RetirementWork
	retirementExecutions  func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]RetirementExecution, error)
	registration          registrationServices
	teamRoster            core.TeamRoster
	rosterPrincipals      identity.TeamRosterPrincipals
	rosterAnalytics       func(context.Context, *gorm.DB, guid.ID, []guid.ID) (map[guid.ID]RosterAnalytics, error)
	profiles              fleet.AgentProfiles
	profileAvatar         identity.AgentAvatar
	memoryPolicy          knowledge.MemoryPolicyValidator
	toolAuthObserver      ToolAuthFailureObserver
	catalogPolicy         fleet.AgentCatalogPolicy
	relay                 relayState
	relayExecution        func(context.Context, *gorm.DB, guid.ID, guid.ID) (RelayExecution, error)
	relayClient           *http.Client
	workspaceMachine      fleet.AgentWorkspaceMachine
	workspace             workspaceRequests
	toolSourceFacts       fleet.ToolSourceFacts
	capabilityExplain     core.AuthorizationExplanation
	reminderCalendar      ReminderCalendar
	reminderWork          delivery.ReminderWorkAccess
	wakeFailureObserver   WakeFailureObserver
	contextReader         ReadContext
	selectedWork          delivery.SelectedAgentWork
	directReader          collab.AgentDirectReader
	runtime               *appenv.Runtime
	config                Config
	names                 query.TableNames
	ids                   guid.Source
	principals            identity.PrincipalStateStore
	members               core.ActiveMemberChecker
	policy                core.EntityConstraintOn
	tenantPolicy          core.TenantConstraintOn
	agents                fleet.AgentDirectory
	runtimeFacts          fleet.AgentRuntimeFacts
	audit                 audit.Appender
	events                events.Publisher
	replay                idempotency.Store
	registrar             *authz.Registrar
	now                   func() time.Time
	registry              *OperationRegistry
	threads               collab.ThreadAccess
	runtimeControl        RuntimeControl
	credentialProof       func(context.Context, *gorm.DB, guid.ID, string, string) error
	fullResetProof        func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error
	forcedRetirementProof func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error
	loopObserver          LoopFireObserver
	activityObserver      ActivityObserver
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	return &Module{runtime: rt, config: config, names: query.BindNames(rt.TablePrefix()), registry: NewOperationRegistry(), now: func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }}
}
func (*Module) ModuleName() string { return Name }

// Initialize registers translations once per application lifecycle, before HTTP publication.
func (*Module) Initialize(context.Context) error { return agentsi18n.Register() }
func (m *Module) Enabled() bool                  { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{iamauthn.Name, collab.Name, delivery.Name, jobs.Name, guid.Name, core.Name, identity.Name, fleet.Name, knowledge.Name, audit.Name, events.Name, idempotency.Name, platformi18n.Name}
}
func (m *Module) InjectServices(s *module.Services) error {
	if err := m.injectPerformance(s); err != nil {
		return err
	}
	if err := m.injectRetirement(s); err != nil {
		return err
	}
	if err := m.injectSourceInbox(s); err != nil {
		return err
	}
	if err := m.injectRoster(s); err != nil {
		return err
	}
	if err := m.injectArchiveResources(s); err != nil {
		return err
	}
	if err := m.injectProfiles(s); err != nil {
		return err
	}
	if err := m.injectRegistration(s); err != nil {
		return err
	}
	if value, e := module.Resolve[fleet.AgentCatalogPolicy](s); e == nil {
		m.catalogPolicy = value
	} else if !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if value, e := module.Resolve[fleet.AgentWorkspaceMachine](s); e == nil {
		m.workspaceMachine = value
	} else if !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if err := m.injectReminderWork(s); err != nil {
		return err
	}
	var err error
	if m.toolSourceFacts, err = module.Resolve[fleet.ToolSourceFacts](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.capabilityExplain, err = module.Resolve[core.AuthorizationExplanation](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.selectedWork, err = module.Resolve[delivery.SelectedAgentWork](s); err != nil {
		return err
	}
	if m.directReader, err = module.Resolve[collab.AgentDirectReader](s); err != nil {
		return err
	}
	if err = m.injectStepUp(s); err != nil {
		return err
	}
	if m.runtimeFacts, err = module.Resolve[fleet.AgentRuntimeFacts](s); err != nil {
		return err
	}
	if m.runtimeFacts.RequestResetOn != nil && m.runtimeFacts.BindResetCompletion != nil {
		if err = m.runtimeFacts.BindResetCompletion(m.completeResetOn); err != nil {
			return err
		}
		if err = m.bindRuntime(RuntimeControl{PrepareOn: m.runtimeFacts.PrepareResetOn, RequestOn: m.runtimeFacts.RequestResetOn}); err != nil {
			return err
		}
	}
	if m.threads, err = module.Resolve[collab.ThreadAccess](s); err != nil {
		return err
	}
	connector, err := module.Resolve[collab.MessageObserverConnector](s)
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrUnavailable
	}
	if err = connector.Bind(m.observeMessageOn); err != nil {
		return err
	}
	if m.ids, err = module.Resolve[guid.Source](s); err != nil {
		return err
	}
	if m.principals, err = module.Resolve[identity.PrincipalStateStore](s); err != nil {
		return err
	}
	if m.members, err = module.Resolve[core.ActiveMemberChecker](s); err != nil {
		return err
	}
	if m.policy, err = module.Resolve[core.EntityConstraintOn](s); err != nil {
		return err
	}
	if m.tenantPolicy, err = module.Resolve[core.TenantConstraintOn](s); err != nil {
		return err
	}
	if m.agents, err = module.Resolve[fleet.AgentDirectory](s); err != nil {
		return err
	}
	if m.audit, err = module.Resolve[audit.Appender](s); err != nil {
		return err
	}
	if m.events, err = module.Resolve[events.Publisher](s); err != nil {
		return err
	}
	if m.replay, err = module.Resolve[idempotency.Store](s); err != nil {
		return err
	}
	if m.registrar, err = module.Resolve[*authz.Registrar](s); err != nil {
		return err
	}
	if m.registrar == nil || m.registrar.Registry() == nil {
		return ErrUnavailable
	}
	if err = m.registrar.Registry().Contribute([]authz.Action{
		{Resource: "agent", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "read agent collaboration state"},
		{Resource: "agent", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "manage agent collaboration protocol"},
		{Resource: "agent", Verb: "workspace_read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "read restricted agent workspace"},
		{Resource: "tool_catalog", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "manage team tools and skills"},
	}, nil); err != nil {
		return err
	}

	catalog, err := module.Resolve[events.Catalog](s)
	if err != nil {
		return err
	}
	return catalog.AddTypes(Name,
		events.TypeSpec{DisplayNameKey: agentsi18n.Namespace + ".activity_event_agents_manifest_published_name", DescriptionKey: agentsi18n.Namespace + ".activity_event_agents_manifest_published_description", Name: "agents.manifest_published", SchemaVersion: 1, SubjectKind: "operation_manifest", Realtime: events.RealtimeNone},
		events.TypeSpec{DisplayNameKey: agentsi18n.Namespace + ".activity_event_agents_changed_name", DescriptionKey: agentsi18n.Namespace + ".activity_event_agents_changed_description", Name: "agents.changed", SchemaVersion: 1, SubjectKind: "agent", Realtime: events.RealtimeNone},
		events.TypeSpec{DisplayNameKey: agentsi18n.Namespace + ".activity_event_agents_wake_name", DescriptionKey: agentsi18n.Namespace + ".activity_event_agents_wake_description", Name: "agents.wake", SchemaVersion: 1, SubjectKind: "agent", Realtime: events.RealtimeNone})
}

func (m *Module) observeMessageOn(ctx context.Context, tx *gorm.DB, r collab.MessageReference) error {
	stream := "channel:" + r.ChannelID.String()
	if r.ThreadID != nil {
		stream = "thread:" + r.ThreadID.String()
	}
	_, err := m.appendOn(ctx, tx, r.TenantID, r.AgentPrincipalID, InboxReference{SourceEventID: "message:" + r.MessageID.String(), Kind: r.Kind, SubjectID: r.MessageID, StreamKey: stream, StreamSeq: r.Seq})
	return err
}

var (
	_ module.Enabler                 = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)

func (m *Module) ProvideServices(s *module.Services) error {
	if err := m.providePerformance(s); err != nil {
		return err
	}
	if err := m.provideRetirement(s); err != nil {
		return err
	}
	if err := m.provideRoster(s); err != nil {
		return err
	}
	if err := m.provideToolRelay(s); err != nil {
		return err
	}
	if err := module.Provide[ManifestPublisher](s, ManifestPublisher{Publish: m.publishManifest}); err != nil {
		return err
	}
	if err := m.provideReminderCalendar(s); err != nil {
		return err
	}
	if err := m.provideWakeNotifications(s); err != nil {
		return err
	}
	if err := m.provideSkillCatalog(s); err != nil {
		return err
	}
	if err := m.provideContextEvidence(s); err != nil {
		return err
	}
	if err := m.provideToolAuthNotifications(s); err != nil {
		return err
	}
	if err := m.provideActivityNotifications(s); err != nil {
		return err
	}
	if err := m.provideLoopReminders(s); err != nil {
		return err
	}
	if err := module.Provide[*OperationRegistry](s, m.registry); err != nil {
		return err
	}
	if err := module.Provide[InboxAppender](s, InboxAppender{AppendOn: m.appendOn}); err != nil {
		return err
	}
	if err := module.Provide[RuntimeControlConnector](s, RuntimeControlConnector{Bind: m.bindRuntime}); err != nil {
		return err
	}
	if err := module.Provide[CredentialProofConnector](s, CredentialProofConnector{Bind: m.bindCredentialProof}); err != nil {
		return err
	}
	return module.Provide[RuntimeUpdates](s, RuntimeUpdates{ReportOn: m.reportOn, CompleteResetOn: m.completeResetOn})
}

var _ module.ServiceProvider = (*Module)(nil)
