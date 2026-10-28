// Package governance owns business authorization and managed role seeding.
package governance

//go:generate go run ./dbgen

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"7link.com/platform/internal/fleet"
	governancei18n "7link.com/platform/internal/governance/i18n"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/infra/realtime"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// Name is distinct from the existing IAM access-governance module.
const Name = "business_governance"

// JobNamespace preserves the business job names frozen by spec08. The module
// lifecycle name differs because IAM already owns the governance module name.
const JobNamespace = "governance"

var ErrRoleNotSeeded = errors.New("business governance role is missing or conflicted")

type Config struct{ module.Config }

// SeededRoles resolves business role keys without exposing governance storage.
// EnsureTenant owns its transaction and must run before a caller's business
// transaction. RoleIDOn reads that caller's transaction; callers authorize their
// business operation and retain responsibility for role/subject compatibility.
type SeededRoles struct {
	EnsureTenant func(context.Context, guid.ID) error
	RoleIDOn     func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error)
}

// Module owns governance records and capabilities.
type Module struct {
	businessAudit      audit.BusinessReader
	escapeEval         EscapeEvalWriter
	temporaryGrants    core.TemporaryGrantReader
	qualityReviews     QualityReviewSource
	pendingRules       knowledge.PendingRules
	spawnRegistration  fleet.AgentRegistrationCommands
	automatedDecisions AutomatedDecisionSource
	learningRules      knowledge.LearningRules
	spawnCosts         spawnCostSources
	localeOnce         sync.Once
	localeErr          error
	spawnFence         dlock.TransactionGuard
	spawnDirectory     fleet.SpawnGrantDirectory
	claimSource        ClaimEvaluationSource
	delegations        delegationServices
	runtime            *appenv.Runtime
	config             Config
	seed               *seedService
	tenants            organization.TenantEnumerator
	queue              jobs.Queue
	explain            core.AuthorizationExplanation
	systemActions      systemActionRegistry
	policies           policyRegistry
	inbox              inboxRegistry
	registrar          *authz.Registrar
	principals         identity.PrincipalStateStore
	publisher          events.Publisher
	reauth             authn.ReauthenticationConsumer
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	return &Module{runtime: rt, config: config, seed: &seedService{names: query.BindNames(rt.TablePrefix()), now: func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }}}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, core.Name, audit.Name, dlock.Name, organization.Name, jobs.Name, identity.Name, events.Name, iamauthn.Name, fleet.Name, idempotency.Name, knowledge.Name, intake.Name, realtime.Name}
}

func (m *Module) ProvideServices(services *module.Services) error {
	if err := m.provideProjectTerminal(services); err != nil {
		return err
	}
	if err := m.provideArchiveEvaluation(services); err != nil {
		return err
	}
	if err := m.provideSpawnModes(services); err != nil {
		return err
	}
	if err := module.Provide[SpawnNotificationFacts](services, m.spawnNotificationFacts); err != nil {
		return err
	}
	if err := module.Provide[IncidentNotificationFacts](services, m.incidentNotificationFacts); err != nil {
		return err
	}
	if err := module.Provide[QualityReviewConnector](services, QualityReviewConnector{Bind: m.bindQualityReviews}); err != nil {
		return err
	}
	if err := module.Provide[AppliedDelegations](services, m.appliedDelegationsOn); err != nil {
		return err
	}
	if err := module.Provide[ProjectLead](services, m.projectLeadOn); err != nil {
		return err
	}
	if err := m.inbox.Register(InboxSource{Kind: "MEMORY_RULE_APPROVAL", Read: m.memoryRuleInbox}); err != nil {
		return err
	}
	if err := m.inbox.Register(InboxSource{Kind: "ESCALATION_ALERT", Read: m.escalationInbox}); err != nil {
		return err
	}
	if err := m.inbox.Register(InboxSource{Kind: "AGENT_SPAWN_APPROVAL", Read: m.agentSpawnRequestInbox}); err != nil {
		return err
	}
	if err := module.Provide[AutomatedDecisionConnector](services, AutomatedDecisionConnector{Bind: m.bindAutomatedDecisions}); err != nil {
		return err
	}
	if err := m.inbox.Register(InboxSource{Kind: "QUALITY_ESCAPE", Read: m.qualityEscapeInbox}); err != nil {
		return err
	}
	if err := module.Provide[DelegationNotificationFacts](services, m.delegationNotificationFacts); err != nil {
		return err
	}
	if err := m.provideSpawnCosts(services); err != nil {
		return err
	}
	if err := module.Provide[SpawnGrantUsage](services, SpawnGrantUsage{Reserve: m.reserveSpawnOn, Release: m.releaseSpawnOn}); err != nil {
		return err
	}
	if err := module.Provide[ClaimEvaluationConnector](services, ClaimEvaluationConnector{Bind: m.bindClaimEvaluationSource}); err != nil {
		return err
	}
	if err := module.Provide[DelegationProjectConnector](services, DelegationProjectConnector{Bind: m.bindDelegationProjects}); err != nil {
		return err
	}
	if err := module.Provide[WorkflowDecisions](services, WorkflowDecisions{RecordOn: m.recordWorkflowDecisionOn, ReadOn: m.readWorkflowDecisionOn}); err != nil {
		return err
	}
	if err := module.Provide[SettingsReader](services, SettingsReader(m.readSettingsOn)); err != nil {
		return err
	}
	if err := module.Provide[EscalationRecorder](services, EscalationRecorder(m.recordEscalationOn)); err != nil {
		return err
	}
	if err := module.Provide[InboxSources](services, &m.inbox); err != nil {
		return err
	}
	if err := module.Provide[DeliveryAuthority](services, DeliveryAuthority(m.deliveryAuthority)); err != nil {
		return err
	}
	if err := module.Provide[ProjectStageAuthority](services, ProjectStageAuthority(m.projectStageAuthority)); err != nil {
		return err
	}
	if err := module.Provide[ProjectCreationAuthority](services, ProjectCreationAuthority(m.projectCreationAuthority)); err != nil {
		return err
	}
	if err := module.Provide[SeededRoles](services, SeededRoles{EnsureTenant: m.ensureTenant, RoleIDOn: m.roleIDOn}); err != nil {
		return err
	}
	if err := module.Provide[SystemActions](services, &m.systemActions); err != nil {
		return err
	}
	if err := m.provideSystemActions(services); err != nil {
		return err
	}
	return module.Provide[PolicyRegistry](services, &m.policies)
}
func (m *Module) InjectServices(services *module.Services) error {
	if m == nil || m.seed == nil {
		return ErrUnavailable
	}
	if err := m.bindInboxRealtime(services); err != nil {
		return err
	}
	if err := m.injectRetirement(services); err != nil {
		return err
	}
	if err := m.injectProductionAuthority(services); err != nil {
		return err
	}
	if err := m.bindAgentSpawn(services); err != nil {
		return err
	}
	if err := m.bindIntakeInbox(services); err != nil {
		return err
	}
	if source, err := module.Resolve[knowledge.PendingRules](services); err == nil {
		m.pendingRules = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[fleet.AgentRegistrationCommands](services); err == nil {
		m.spawnRegistration = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if rules, err := module.Resolve[knowledge.LearningRules](services); err == nil {
		m.learningRules = rules
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}

	if err := m.bindAutoDispatch(services); err != nil {
		return err
	}
	if err := m.injectDelegations(services); err != nil {
		return err
	}
	var spawnErr error
	if m.spawnDirectory, spawnErr = module.Resolve[fleet.SpawnGrantDirectory](services); spawnErr != nil {
		return spawnErr
	}
	if m.spawnFence, spawnErr = module.Resolve[dlock.TransactionGuard](services); spawnErr != nil {
		return spawnErr
	}
	subscribers, subscriberErr := module.Resolve[events.SubscriberCatalog](services)
	if subscriberErr != nil {
		return subscriberErr
	}
	if subscriberErr = subscribers.AddSubscribers(Name, events.Subscriber{DisplayNameKey: governancei18n.Namespace + ".activity_subscriber_business_governance_automated_decisions_name", DescriptionKey: governancei18n.Namespace + ".activity_subscriber_business_governance_automated_decisions_description", Name: "business_governance.automated_decisions", EventTypes: []string{"work_item.state_changed"}, Mode: events.Transactional, StartFrom: events.Earliest, Handle: m.consumeAutomatedDecision}); subscriberErr != nil {
		return subscriberErr
	}
	if subscriberErr = subscribers.AddSubscribers(Name, events.Subscriber{DisplayNameKey: governancei18n.Namespace + ".activity_subscriber_business_governance_quality_escapes_name", DescriptionKey: governancei18n.Namespace + ".activity_subscriber_business_governance_quality_escapes_description", Name: "business_governance.quality_escapes", EventTypes: []string{"quality.escaped", "quality.escape_learning_proposed"}, Mode: events.Transactional, StartFrom: events.Earliest, Handle: m.consumeQualityEscape}); subscriberErr != nil {
		return subscriberErr
	}
	if subscriberErr = subscribers.AddSubscribers(Name, events.Subscriber{DisplayNameKey: governancei18n.Namespace + ".activity_subscriber_business_governance_spawn_settlements_name", DescriptionKey: governancei18n.Namespace + ".activity_subscriber_business_governance_spawn_settlements_description", Name: "business_governance.spawn_settlements", EventTypes: []string{"budget.settled"}, Mode: events.Transactional, StartFrom: events.Earliest, Handle: m.consumeSpawnSettlement}); subscriberErr != nil {
		return subscriberErr
	}
	if subscriberErr = subscribers.AddSubscribers(Name, events.Subscriber{DisplayNameKey: governancei18n.Namespace + ".activity_subscriber_business_governance_escalation_evaluator_name", DescriptionKey: governancei18n.Namespace + ".activity_subscriber_business_governance_escalation_evaluator_description", Name: "business_governance.escalation_evaluator", EventTypes: []string{"claim.submitted", "work_item.state_changed"}, Mode: events.Transactional, StartFrom: events.Earliest, Handle: m.evaluateClaimEvent}); subscriberErr != nil {
		return subscriberErr
	}
	var err error
	if m.principals, err = module.Resolve[identity.PrincipalStateStore](services); err != nil {
		return err
	}
	if m.publisher, err = module.Resolve[events.Publisher](services); err != nil {
		return err
	}
	if m.reauth, err = module.Resolve[authn.ReauthenticationConsumer](services); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.Catalog](services)
	if err != nil {
		return err
	}
	if err = catalog.AddTypes(Name, events.TypeSpec{DisplayNameKey: governancei18n.Namespace + ".activity_event_governance_inbox_acknowledged_name", DescriptionKey: governancei18n.Namespace + ".activity_event_governance_inbox_acknowledged_description", Name: "governance.inbox_acknowledged", SchemaVersion: 1, SubjectKind: "inbox_item", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: governancei18n.Namespace + ".activity_event_governance_spawn_request_changed_name", DescriptionKey: governancei18n.Namespace + ".activity_event_governance_spawn_request_changed_description", Name: "governance.spawn_request_changed", SchemaVersion: 1, SubjectKind: "spawn_request", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: governancei18n.Namespace + ".activity_event_governance_automated_decision_recorded_name", DescriptionKey: governancei18n.Namespace + ".activity_event_governance_automated_decision_recorded_description", Name: "governance.automated_decision_recorded", SchemaVersion: 1, SubjectKind: "automated_decision", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: governancei18n.Namespace + ".activity_event_governance_workflow_decided_name", DescriptionKey: governancei18n.Namespace + ".activity_event_governance_workflow_decided_description", Name: "governance.workflow_decided", SchemaVersion: 1, SubjectKind: "workflow_decision", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: governancei18n.Namespace + ".activity_event_governance_settings_updated_name", DescriptionKey: governancei18n.Namespace + ".activity_event_governance_settings_updated_description", Name: "governance.settings_updated", SchemaVersion: 1, SubjectKind: "governance_settings", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: governancei18n.Namespace + ".activity_event_governance_rule_updated_name", DescriptionKey: governancei18n.Namespace + ".activity_event_governance_rule_updated_description", Name: "governance.rule_updated", SchemaVersion: 1, SubjectKind: "escalation_rule", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: governancei18n.Namespace + ".activity_event_governance_incident_changed_name", DescriptionKey: governancei18n.Namespace + ".activity_event_governance_incident_changed_description", Name: "governance.incident_changed", SchemaVersion: 1, SubjectKind: "escalation_incident", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	for _, kind := range []string{"spawn_grant.granted", "spawn_grant.updated", "spawn_grant.exhausted", "spawn_grant.revoked", "spawn_grant.expired", "spawn_grant.budget_threshold"} {
		if err = catalog.AddTypes(Name, events.TypeSpec{DisplayNameKey: governancei18n.Namespace + ".activity_event_" + strings.ReplaceAll(kind, ".", "_") + "_name", DescriptionKey: governancei18n.Namespace + ".activity_event_" + strings.ReplaceAll(kind, ".", "_") + "_description", Name: kind, SchemaVersion: 1, SubjectKind: "spawn_grant", Realtime: events.RealtimeSignal}); err != nil {
			return err
		}
	}
	if err = catalog.AddTypes(Name, events.TypeSpec{DisplayNameKey: governancei18n.Namespace + ".activity_event_quality_escape_recorded_name", DescriptionKey: governancei18n.Namespace + ".activity_event_quality_escape_recorded_description", Name: "quality.escape_recorded", SchemaVersion: 1, SubjectKind: "quality_escape", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: governancei18n.Namespace + ".activity_event_quality_escape_learning_decided_name", DescriptionKey: governancei18n.Namespace + ".activity_event_quality_escape_learning_decided_description", Name: "quality.escape_learning_decided", SchemaVersion: 1, SubjectKind: "quality_escape", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if m.seed.ids, err = module.Resolve[guid.Source](services); err != nil {
		return err
	}
	if m.seed.roles, err = module.Resolve[core.RoleSeeder](services); err != nil {
		return err
	}
	if m.explain, err = module.Resolve[core.AuthorizationExplanation](services); err != nil {
		return err
	}
	if m.businessAudit, err = module.Resolve[audit.BusinessReader](services); err != nil {
		return err
	}
	if m.seed.audit, err = module.Resolve[audit.Appender](services); err != nil {
		return err
	}
	if m.seed.acquire, err = module.Resolve[dlock.TransactionLease](services); err != nil {
		return err
	}
	if m.tenants, err = module.Resolve[organization.TenantEnumerator](services); err != nil {
		return err
	}
	if m.queue, err = module.Resolve[jobs.Queue](services); err != nil {
		return err
	}
	registrar, err := module.Resolve[*authz.Registrar](services)
	if err != nil {
		return err
	}
	m.seed.registry = registrar.Registry()
	m.registrar = registrar
	if m.seed.registry == nil {
		return nil // Initialize retains the existing missing-registry failure.
	}
	if err := m.seed.registry.Contribute([]authz.Action{
		{Resource: "spawn_grant", Verb: "manage", Scope: authz.ActionScopeTenant, Summary: "Manage bounded Agent spawn grants"},
		{Resource: "agent", Verb: "spawn", Scope: authz.ActionScopeTenant, Summary: "Create an Agent through a bounded policy grant"},
		{Resource: "delegation", Verb: "grant", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Grant and revoke bounded project delegation"},
		{Resource: "workflow_gate", Verb: "team", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Record team workflow gate decisions"},
		{Resource: "workflow_gate", Verb: "client", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Record client workflow gate decisions"},
		{Resource: "brief", Verb: "sign", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Sign requirement confirmation sheets"},
		{Resource: "policy", Verb: "read", Scope: authz.ActionScopeTenant, Menu: true, Summary: "Read business authorization policies"},
		{Resource: "policy", Verb: "manage", Scope: authz.ActionScopeTenant, Summary: "Manage business authorization policies"},
		{Resource: "audit", Verb: "read", Scope: authz.ActionScopeTenant, Menu: true, Summary: "Read business audit events"},
	}, nil); err != nil {
		return err
	}
	return nil
}
func (m *Module) Initialize(context.Context) error {
	if m == nil || m.seed == nil || m.seed.ids == nil || m.seed.roles.EnsureRoleOn == nil || m.seed.roles.GrantPermissionOn == nil || m.seed.roles.GrantConditionalPermissionOn == nil || m.seed.roles.SeedAdministratorMembersOn == nil || m.seed.audit == nil || m.seed.acquire == nil || m.seed.registry == nil || m.explain == nil {
		return ErrUnavailable
	}
	m.localeOnce.Do(func() { m.localeErr = governancei18n.Register() })
	if m.localeErr != nil {
		return m.localeErr
	}
	if err := m.systemActions.seal(m.seed.registry); err != nil {
		return err
	}
	return m.policies.seal(m.seed.registry)
}

// JobHandlers contributes the platform-wide role reconciliation sweep.
func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{{DisplayNameKey: governancei18n.Namespace + ".activity_job_governance_seed_reconcile_name", DescriptionKey: governancei18n.Namespace + ".activity_job_governance_seed_reconcile_description", Kind: JobNamespace + ".seed_reconcile", Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID != 0 {
			return jobs.Permanent(ErrInvalidArgument)
		}
		return m.reconcileTenants(ctx)
	}}, {DisplayNameKey: governancei18n.Namespace + ".activity_job_governance_delegation_expiry_name", DescriptionKey: governancei18n.Namespace + ".activity_job_governance_delegation_expiry_description", Kind: JobNamespace + ".delegation_expiry", Run: m.expireDelegations}, {DisplayNameKey: governancei18n.Namespace + ".activity_job_governance_spawn_grant_reconcile_name", DescriptionKey: governancei18n.Namespace + ".activity_job_governance_spawn_grant_reconcile_description", Kind: JobNamespace + ".spawn_grant_reconcile", Run: m.reconcileSpawnGrants}, {DisplayNameKey: governancei18n.Namespace + ".activity_job_governance_spawn_request_reconcile_name", DescriptionKey: governancei18n.Namespace + ".activity_job_governance_spawn_request_reconcile_description", Kind: JobNamespace + ".spawn_request_reconcile", Run: m.reconcileAgentSpawnRequests}, {DisplayNameKey: governancei18n.Namespace + ".activity_job_governance_delegation_activation_name", DescriptionKey: governancei18n.Namespace + ".activity_job_governance_delegation_activation_description", Kind: JobNamespace + ".delegation_activation", Run: m.activateScheduledDelegations}}
}

// JobSchedules declares hourly tenant role reconciliation. The platform sweep
// owns tenant paging itself rather than using the active-tenant fanout handler.
func (*Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: JobNamespace + ".seed_reconcile", Kind: JobNamespace + ".seed_reconcile", Interval: time.Hour, TenantScope: jobs.ScopePlatform}, {Name: JobNamespace + ".delegation_expiry", Kind: JobNamespace + ".delegation_expiry", Interval: 30 * time.Second, TenantScope: jobs.ScopePlatform}, {Name: JobNamespace + ".spawn_grant_reconcile", Kind: JobNamespace + ".spawn_grant_reconcile", Interval: 30 * time.Second, TenantScope: jobs.ScopePlatform}, {Name: JobNamespace + ".spawn_request_reconcile", Kind: JobNamespace + ".spawn_request_reconcile", Interval: 30 * time.Second, TenantScope: jobs.ScopePlatform}, {Name: JobNamespace + ".delegation_activation", Kind: JobNamespace + ".delegation_activation", Interval: 30 * time.Second, TenantScope: jobs.ScopePlatform}}
}

// Start persists one startup sweep per role manifest. Replicas and restarts
// share the durable queue receipt; the hourly schedule owns subsequent sweeps.
// Queue pressure must not prevent workers from starting and draining the queue.
func (m *Module) Start(ctx context.Context) error {
	if m == nil || m.queue == nil {
		return ErrUnavailable
	}
	kind := JobNamespace + ".seed_reconcile"
	_, err := m.queue.Enqueue(ctx, nil, jobs.EnqueueRequest{Kind: kind, ConcurrencyKey: kind, IdempotencyKey: fmt.Sprintf("startup:manifest:%d", RoleManifestVersion)})
	if errors.Is(err, jobs.ErrQueueFull) {
		slog.WarnContext(ctx, "governance startup reconciliation deferred; hourly schedule will retry", "kind", kind, "error", err)
		return nil
	}
	return err
}
func (m *Module) ensureTenant(ctx context.Context, tenantID guid.ID) error {
	if err := m.Initialize(ctx); err != nil {
		return err
	}
	if m.runtime == nil || m.runtime.Database() == nil {
		return ErrUnavailable
	}
	return m.seed.apply(ctx, m.runtime.Database().Write(), tenantID, RoleManifestVersion, defaultRoleManifest())
}

// reconcileTenants is the shared startup/scheduled sweep. Each tenant commits
// independently; retrying a failed sweep is safe because seeding is idempotent.
// A tenant held by another seeder is skipped only for this background sweep.
// Synchronous first access still receives ErrNotAcquired from ensureTenant.
func (m *Module) reconcileTenants(ctx context.Context) error {
	if m == nil || m.tenants == nil {
		return ErrUnavailable
	}
	const pageSize = 100
	var after guid.ID
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tenants, err := m.tenants(ctx, after, pageSize)
		if err != nil {
			return fmt.Errorf("enumerate tenants for role seeding: %w", err)
		}
		for _, tenantID := range tenants {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := m.ensureTenant(ctx, tenantID); err != nil && !errors.Is(err, dlock.ErrNotAcquired) {
				return fmt.Errorf("seed tenant %s: %w", tenantID, err)
			}
		}
		if len(tenants) < pageSize {
			return nil
		}
		after = tenants[len(tenants)-1]
	}
}
func (m *Module) roleIDOn(ctx context.Context, tx *gorm.DB, tenantID guid.ID, key string) (guid.ID, error) {
	if m == nil || m.seed == nil || m.seed.names.RoleSeeds.IsZero() {
		return 0, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenantID <= 0 || key == "" {
		return 0, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	table := query.BindRoleSeeds(tx, m.seed.names)
	row, err := table.WithContext(ctx).Where(table.TenantID.Eq(tenantID), table.RoleKey.Eq(key)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrRoleNotSeeded
	}
	if err != nil {
		return 0, err
	}
	// Detaching a role from upgrades does not revoke its business identity.
	if row.SeedConflict || row.RoleID <= 0 {
		return 0, ErrRoleNotSeeded
	}
	return row.RoleID, nil
}

var (
	_ module.Enabler                 = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.Starter                 = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
	_ module.HTTPRegistrar           = (*Module)(nil)
	_ jobs.HandlerProvider           = (*Module)(nil)
	_ jobs.ScheduleProvider          = (*Module)(nil)
)

func (*Module) JobNamespace() string { return JobNamespace }
