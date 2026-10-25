// Package yunxiao owns Yunxiao connection configuration and synchronization mappings.
package yunxiao

//go:generate go run ./dbgen

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/webhook"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/intake"
	yi18n "7link.com/platform/internal/yunxiao/i18n"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"log/slog"
	"time"
)

const Name = "yunxiao"

type Config struct {
	module.Config
	EncryptionKey              string   `mapstructure:"encryption_key"`
	EncryptionKeyFile          string   `mapstructure:"encryption_key_file"`
	PreviousEncryptionKeys     []string `mapstructure:"previous_encryption_keys"`
	PreviousEncryptionKeyFiles []string `mapstructure:"previous_encryption_key_files"`
}
type Module struct {
	intakeCallbacks            webhook.IntakeCallbackSubscriptions
	workflowStates             delivery.WorkflowDisplayStates
	ingressFiles               ingressPayloadStore
	ingressQueue               jobs.Queue
	ingressAdmission           jobs.QueueAdmission
	planningWork               delivery.PlanningWorkIdentityOn
	runtime                    *appenv.Runtime
	runtimeFacts               RuntimeReader
	automationEvents           events.EventReader
	automationNotify           func(context.Context, *gorm.DB, AutomationNotice) error
	automationReport           func(context.Context, *gorm.DB, AutomationReportEntry) error
	automationAssignment       delivery.AutomationAssignmentOn
	automationDispatch         *delivery.AutomationDispatchCommands
	automationLinkedItem       delivery.AutomationLinkedItemOn
	automationSystemAllowed    governance.SystemActionAllowed
	automationOwner            delivery.AutomationWorkOwnerOn
	automationTransition       delivery.AutomationTransitionOn
	automationPreparationField delivery.AutomationPreparationFieldOn
	automationTriggerWork      delivery.AutomationTriggerWorkOn
	automationVerification     delivery.AutomationVerificationOn
	automationWorkParticipants delivery.AutomationWorkParticipantsOn
	workItemOn                 func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
	effortWorkOn               delivery.WorkItemVisibilityOn
	config                     Config
	names                      query.TableNames
	ids                        guid.Source
	principals                 identity.PrincipalStateStore
	members                    core.ActiveMemberChecker
	policy                     core.TenantConstraintOn
	projects                   delivery.ProjectScope
	intakeReceiving            intake.APIReceiving
	audit                      audit.Appender
	events                     events.Publisher
	idem                       idempotency.Store
	proof                      authn.ReauthenticationConsumer
	registrar                  *authz.Registrar
	now                        func() time.Time
}

func NewModule(rt *appenv.Runtime, c Config) *Module {
	prefix := ""
	if rt != nil {
		prefix = rt.TablePrefix()
	}
	return &Module{runtime: rt, config: c, names: query.BindNames(prefix), now: func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, identity.Name, core.Name, delivery.Name, files.Name, intake.Name, governance.Name, audit.Name, events.Name, jobs.Name, idempotency.Name, iamauthn.Name}
}
func (m *Module) InjectServices(s *module.Services) error {
	if err := m.bindIngress(s); err != nil {
		return err
	}
	if err := m.bindIntakeTiming(s); err != nil {
		return err
	}
	var err error
	if err = m.registerAutomationSystemActions(s); err != nil {
		return err
	}
	if m.automationLinkedItem, err = module.Resolve[delivery.AutomationLinkedItemOn](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.automationDispatch, err = module.Resolve[*delivery.AutomationDispatchCommands](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.automationAssignment, err = module.Resolve[delivery.AutomationAssignmentOn](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.automationPreparationField, err = module.Resolve[delivery.AutomationPreparationFieldOn](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.automationWorkParticipants, err = module.Resolve[delivery.AutomationWorkParticipantsOn](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.automationVerification, err = module.Resolve[delivery.AutomationVerificationOn](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.automationTriggerWork, err = module.Resolve[delivery.AutomationTriggerWorkOn](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.automationTransition, err = module.Resolve[delivery.AutomationTransitionOn](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.planningWork, err = module.Resolve[delivery.PlanningWorkIdentityOn](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.automationOwner, err = module.Resolve[delivery.AutomationWorkOwnerOn](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.automationEvents, err = module.Resolve[events.EventReader](s); err != nil {
		return err
	}
	if m.intakeReceiving, err = module.Resolve[intake.APIReceiving](s); err != nil {
		return err
	}
	if m.workflowStates, err = module.Resolve[delivery.WorkflowDisplayStates](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.workflowStates.BindResolver != nil {
		if err := m.workflowStates.BindResolver(m.workflowDisplayKeyOn); err != nil {
			return err
		}
	}
	workflow, err := module.Resolve[delivery.WorkflowTransitionConnector](s)
	if err != nil {
		return err
	}
	if workflow.Bind == nil {
		return ErrUnavailable
	}
	if err = workflow.Bind(m.checkTransitionOn); err != nil {
		return err
	}
	if workflow.BindSelection != nil {
		if err = workflow.BindSelection(m.selectWorkflowStateOn); err != nil {
			return err
		}
	}
	m.workItemOn = workflow.WorkItemOn
	visibility, err := module.Resolve[delivery.WorkItemVisibilityOn](s)
	if err != nil {
		return err
	}
	if visibility == nil {
		return ErrUnavailable
	}
	m.effortWorkOn = func(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) error {
		err := visibility(ctx, tx, tenant, project, work)
		switch {
		case errors.Is(err, delivery.ErrProjectNotFound):
			return ErrNotFound
		case errors.Is(err, delivery.ErrWorkForbidden):
			return ErrForbidden
		case errors.Is(err, delivery.ErrUnavailable):
			return ErrUnavailable
		default:
			return err
		}
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
	if m.policy, err = module.Resolve[core.TenantConstraintOn](s); err != nil {
		return err
	}
	if m.projects, err = module.Resolve[delivery.ProjectScope](s); err != nil {
		return err
	}
	if m.audit, err = module.Resolve[audit.Appender](s); err != nil {
		return err
	}
	if m.events, err = module.Resolve[events.Publisher](s); err != nil {
		return err
	}
	if m.idem, err = module.Resolve[idempotency.Store](s); err != nil {
		return err
	}
	if m.proof, err = module.Resolve[authn.ReauthenticationConsumer](s); err != nil {
		return err
	}
	if m.registrar, err = module.Resolve[*authz.Registrar](s); err != nil {
		return err
	}
	if m.registrar == nil || m.registrar.Registry() == nil {
		return ErrUnavailable
	}
	if err = m.registrar.Registry().Contribute([]authz.Action{{Resource: "intake", Verb: "submit", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Submit authenticated API intake"}}, nil); err != nil {
		return err
	}
	if err = m.registrar.Registry().Contribute([]authz.Action{{Resource: "intake_channel", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Manage intake channel settings"}, {Resource: "intake", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Read intake channel settings"}, {Resource: "worklog", Verb: "submit", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Submit own worklogs"}, {Resource: "worklog", Verb: "approve", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Approve another human worklogs"}, {Resource: "workflow", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Manage workflow matrix"}, {Resource: "integration", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Manage external integrations"}}, nil); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.Catalog](s)
	if err != nil {
		return err
	}
	if err = catalog.AddTypes(Name, events.TypeSpec{Name: "integration.ingress_changed", SchemaVersion: 1, SubjectKind: "integration_ingress", Realtime: events.RealtimeNone}); err != nil {
		return err
	}
	subscribers, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	if err = subscribers.AddSubscribers(Name, events.Subscriber{Name: "yunxiao.agent_worklog", EventTypes: []string{"execution.state_changed"}, Mode: events.Transactional, StartFrom: events.Earliest, Handle: m.consumeRuntime}); err != nil {
		return err
	}
	if err = subscribers.AddSubscribers(Name, events.Subscriber{Name: "yunxiao.automation_rules", EventTypes: automationTriggerTypes, Mode: events.Transactional, StartFrom: events.Latest, Handle: m.consumeAutomation}); err != nil {
		return err
	}
	if err = catalog.AddTypes(Name, events.TypeSpec{Name: "automation.schedule_triggered", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeNone}); err != nil {
		return err
	}
	if err = m.registerWorklogInbox(s); err != nil {
		return err
	}
	return catalog.AddTypes(Name, events.TypeSpec{Name: "worklog.confirmed", SchemaVersion: 1, SubjectKind: "worklog", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "planning.entity_changed", SchemaVersion: 1, SubjectKind: "planning_entity", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "planning.assignment_changed", SchemaVersion: 1, SubjectKind: "planning_assignment", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "automation.rule_registered", SchemaVersion: 1, SubjectKind: "automation_rule", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "automation.run_failed", SchemaVersion: 1, SubjectKind: "automation_run", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "intake.channel_configured", SchemaVersion: 1, SubjectKind: "intake_channel", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "worklog.saved", SchemaVersion: 1, SubjectKind: "worklog", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "worklog.submitted", SchemaVersion: 1, SubjectKind: "worklog", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "worklog.approved", SchemaVersion: 1, SubjectKind: "worklog", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "worklog.returned", SchemaVersion: 1, SubjectKind: "worklog", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "worklog.withdrawn", SchemaVersion: 1, SubjectKind: "worklog", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "workflow.config_changed", SchemaVersion: 1, SubjectKind: "workflow_config", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "integration.configuration_changed", SchemaVersion: 1, SubjectKind: "integration", Realtime: events.RealtimeSignal})
}
func (m *Module) Initialize(ctx context.Context) error {
	if m == nil || m.ids == nil || m.policy == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.members == nil || m.projects.AuthorizeOn == nil || m.audit == nil || m.events == nil || m.idem == nil || m.proof == nil || m.now == nil {
		return ErrUnavailable
	}
	if err := yi18n.Register(); err != nil {
		return err
	}
	slog.WarnContext(ctx, "Yunxiao remote operations unavailable; verify and install the provider API contract before enabling synchronization")
	return nil
}

var (
	_ module.Enabler                 = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.HTTPRegistrar           = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)
