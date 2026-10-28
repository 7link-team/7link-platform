// Package fleet owns Agent identities and runtime infrastructure.
package fleet

//go:generate go run ./dbgen

import (
	"7link.com/platform/internal/files"
	fleeti18n "7link.com/platform/internal/fleet/i18n"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

const Name = "fleet"

type Config struct {
	Runner  RunnerInstallConfig `mapstructure:"runner"`
	Preview PreviewConfig       `mapstructure:"preview"`
	module.Config
	GatewayEncryptionKey          string   `mapstructure:"gateway_encryption_key" secret:"true"`
	GatewayEncryptionKeyFile      string   `mapstructure:"gateway_encryption_key_file"`
	PreviousGatewayEncryptionKeys []string `mapstructure:"previous_gateway_encryption_keys" secret:"true"`
}

// Module owns fleet records and exposes identity facts to business consumers.
type Module struct {
	machineHistory        MachineExecutionHistoryReader
	workdirQuiet          func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
	processObserver       func(context.Context, *gorm.DB, AgentProcessTransition) error
	retirementObserver    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
	previewBroker         *previewBroker
	previewTransport      *PreviewRuntime
	previewAccess         PreviewAccessResolver
	terminal              terminalServices
	terminalBroker        *terminalBroker
	spawnPolicy           AgentSpawnPolicy
	agentRebindQuiet      func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)
	agentCredentialRotate identity.AgentCredentialRotator
	agentRoles            func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) error
	ensemble              EnsembleSources
	resetComplete         func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error
	idempotency           idempotency.Store
	runtime               *appenv.Runtime
	policy                core.TenantConstraintOn
	repository            RepositoryAuthorizer
	registrar             *authz.Registrar
	enrollmentIAM         EnrollmentIAM
	enrollmentLease       dlock.RenewingLease
	config                Config
	names                 query.TableNames
	ids                   guid.Source
	principals            identity.PrincipalStateStore
	accounts              identity.ServiceAccountValidator
	audit                 audit.Appender
	events                events.Publisher
	revokeProof           authnext.ReauthenticationConsumer
	revokeAccount         identity.ServiceAccountDisabler
	revokeBrowser         authnext.BrowserSessionSecurity
	gatewayBudgets        GatewayBudgetConsumer
	gatewaySettlement     GatewaySettlement
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	return &Module{runtime: rt, config: config, names: query.BindNames(rt.TablePrefix())}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (m *Module) ProvideServices(s *module.Services) error {
	if err := m.provideMachineHistory(s); err != nil {
		return err
	}
	if err := module.Provide[WorkdirLifecycleConnector](s, WorkdirLifecycleConnector{Bind: m.bindWorkdirQuiet}); err != nil {
		return err
	}
	if err := m.provideArchiveVerifiers(s); err != nil {
		return err
	}
	if err := m.provideRetirementObservers(s); err != nil {
		return err
	}
	if err := m.provideWorkspace(s); err != nil {
		return err
	}
	if err := m.provideToolSources(s); err != nil {
		return err
	}
	if err := m.provideTerminal(s); err != nil {
		return err
	}
	if err := m.providePreview(s); err != nil {
		return err
	}
	if err := m.provideGatewaySettlement(s); err != nil {
		return err
	}
	if err := m.provideAgentSpawn(s); err != nil {
		return err
	}
	if err := m.provideAgentRebind(s); err != nil {
		return err
	}
	if err := m.provideWorkbenchFacts(s); err != nil {
		return err
	}
	if err := m.provideAgentRegistration(s); err != nil {
		return err
	}
	if err := m.provideEnsembles(s); err != nil {
		return err
	}
	if err := m.provideSpawnDirectory(s); err != nil {
		return err
	}
	if err := m.provideDispatch(s); err != nil {
		return err
	}
	if err := module.Provide[RealtimeTopics](s, RealtimeTopics{Authorize: m.authorizeRealtimeMachine}); err != nil {
		return err
	}
	if err := module.Provide[RotationNotificationFacts](s, m.rotationNotificationFacts); err != nil {
		return err
	}
	if err := module.Provide[RotationNotificationRequester](s, m.rotationNotificationRequester); err != nil {
		return err
	}
	if err := m.provideGatewayBudgets(s); err != nil {
		return err
	}
	if err := m.provideAgentRuntime(s); err != nil {
		return err
	}
	if err := module.Provide[RepositoryConnector](s, RepositoryConnector{Bind: m.bindRepository}); err != nil {
		return err
	}
	if err := module.Provide[AgentDirectory](s, AgentDirectory{ByPrincipalOn: m.ByPrincipalOn, ActiveByRoleOn: m.ActiveByRoleOn}); err != nil {
		return err
	}
	return module.Provide[MachineDirectory](s, MachineDirectory{ModelFamilyOn: m.ModelFamilyOn, BindRunnerContext: m.BindRunnerContext, ModelPriceOn: m.ModelPriceOn, ValidateSessionOn: m.HeartbeatOn, LockOn: m.LockMachineOn, ReacquireSlotOn: m.ReacquireSlotOn, ByIDOn: m.MachineByIDOn, ResolveWorkspaceOn: m.ResolveWorkspaceOn, ReserveSlotOn: m.ReserveSlotOn, ReleaseSlotOn: m.ReleaseSlotOn})
}

func (*Module) ModuleDependencies() []string {
	return []string{files.Name, guid.Name, identity.Name, audit.Name, events.Name, core.Name, jobs.Name, platformi18n.Name, IAMBridgeName, dlock.Name, idempotency.Name, authenticationModule}
}
func (m *Module) InjectServices(s *module.Services) error {
	if err := m.injectTerminal(s); err != nil {
		return err
	}
	if err := m.injectAgentRebind(s); err != nil {
		return err
	}
	if err := m.injectGatewayBudgets(s); err != nil {
		return err
	}
	var err error
	if m.idempotency, err = module.Resolve[idempotency.Store](s); err != nil {
		return err
	}
	if m.enrollmentLease, err = module.Resolve[dlock.RenewingLease](s); err != nil {
		return err
	}
	if m.enrollmentIAM, err = module.Resolve[EnrollmentIAM](s); err != nil {
		return err
	}
	if m.policy, err = module.Resolve[core.TenantConstraintOn](s); err != nil {
		return err
	}
	if m.registrar, err = module.Resolve[*authz.Registrar](s); err != nil {
		return err
	}
	if err = m.registerTerminalActions(); err != nil {
		return err
	}
	if err = m.registrar.Registry().Contribute([]authz.Action{
		{Resource: "gateway", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "manage model gateway"},
		{Resource: "machine", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "read machines"},
		{Resource: "machine", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "manage machines"},
		{Resource: "machine", Verb: "revoke", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "revoke machines"},
	}, nil); err != nil {
		return err
	}
	if m.ids, err = module.Resolve[guid.Source](s); err != nil {
		return err
	}
	if m.principals, err = module.Resolve[identity.PrincipalStateStore](s); err != nil {
		return err
	}
	if m.accounts, err = module.Resolve[identity.ServiceAccountValidator](s); err != nil {
		return err
	}
	if m.audit, err = module.Resolve[audit.Appender](s); err != nil {
		return err
	}
	m.bindAudit()
	if m.events, err = module.Resolve[events.Publisher](s); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.Catalog](s)
	if err != nil {
		return err
	}
	if err := m.injectRetirement(s); err != nil {
		return err
	}
	return catalog.AddTypes(Name,
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_machine_runtimes_changed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_machine_runtimes_changed_description", Name: "machine.runtimes_changed", SchemaVersion: 1, SubjectKind: "machine", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_fleet_model_price_changed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_fleet_model_price_changed_description", Name: "fleet.model_price_changed", SchemaVersion: 1, SubjectKind: "model_price", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_agent_spawned_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_agent_spawned_description", Name: "agent.spawned", SchemaVersion: 1, SubjectKind: "agent", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_agent_retired_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_agent_retired_description", Name: "agent.retired", SchemaVersion: 1, SubjectKind: "agent", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_fleet_agent_rebound_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_fleet_agent_rebound_description", Name: "fleet.agent_rebound", SchemaVersion: 1, SubjectKind: "agent", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_machine_agent_command_completed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_machine_agent_command_completed_description", Name: "machine.agent_command_completed", SchemaVersion: 1, SubjectKind: "machine", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_fleet_agent_registered_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_fleet_agent_registered_description", Name: "fleet.agent_registered", SchemaVersion: 1, SubjectKind: "agent", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_fleet_agent_presence_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_fleet_agent_presence_description", Name: "fleet.agent_presence", SchemaVersion: 1, SubjectKind: "agent", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_agent_crashed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_agent_crashed_description", Name: "agent.crashed", SchemaVersion: 1, SubjectKind: "agent", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_agent_restart_exhausted_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_agent_restart_exhausted_description", Name: "agent.restart_exhausted", SchemaVersion: 1, SubjectKind: "agent", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_ensemble_changed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_ensemble_changed_description", Name: "ensemble.changed", SchemaVersion: 1, SubjectKind: "ensemble_plan", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_gateway_config_changed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_gateway_config_changed_description", Name: "gateway.config_changed", SchemaVersion: 1, SubjectKind: "model_gateway", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_gateway_key_created_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_gateway_key_created_description", Name: "gateway.key_created", SchemaVersion: 1, SubjectKind: "model_gateway", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_gateway_key_rotated_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_gateway_key_rotated_description", Name: "gateway.key_rotated", SchemaVersion: 1, SubjectKind: "gateway_key", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_gateway_request_changed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_gateway_request_changed_description", Name: "gateway.request_changed", SchemaVersion: 1, SubjectKind: "model_gateway", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_terminal_changed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_terminal_changed_description", Name: "terminal.changed", SchemaVersion: 1, SubjectKind: "terminal_session", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_machine_access_changed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_machine_access_changed_description", Name: "machine.access_changed", SchemaVersion: 1, SubjectKind: "machine", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_machine_inspection_changed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_machine_inspection_changed_description", Name: "machine.inspection_changed", SchemaVersion: 1, SubjectKind: "machine", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_machine_credential_changed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_machine_credential_changed_description", Name: "machine.credential_changed", SchemaVersion: 1, SubjectKind: "machine", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_machine_enrollment_changed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_machine_enrollment_changed_description", Name: "machine.enrollment_changed", SchemaVersion: 1, SubjectKind: "machine_enrollment", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_machine_inventory_updated_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_machine_inventory_updated_description", Name: "machine.inventory_updated", SchemaVersion: 1, SubjectKind: "machine", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_machine_state_changed_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_machine_state_changed_description", Name: "machine.state_changed", SchemaVersion: 1, SubjectKind: "machine", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_machine_slot_reserved_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_machine_slot_reserved_description", Name: "machine.slot_reserved", SchemaVersion: 1, SubjectKind: "machine", Realtime: events.RealtimeSignal},
		events.TypeSpec{DisplayNameKey: fleeti18n.Namespace + ".activity_event_machine_slot_released_name", DescriptionKey: fleeti18n.Namespace + ".activity_event_machine_slot_released_description", Name: "machine.slot_released", SchemaVersion: 1, SubjectKind: "machine", Realtime: events.RealtimeSignal})
}
func (m *Module) Initialize(ctx context.Context) error {

	if m == nil || m.ids == nil || m.principals.Lock == nil || m.accounts == nil || m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	if err := m.initializePreview(ctx); err != nil {
		return err
	}
	return fleeti18n.Register()
}

func (*Module) ModuleName() string { return Name }

var (
	_ module.MigrationSourceProvider = (*Module)(nil)
	_ module.Enabler                 = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
)
