// Package notification owns the durable in-app inbox and delivery preferences.
package notification

//go:generate go run ./dbgen
import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/infra/realtime"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/knowledge"
	notificationi18n "7link.com/platform/internal/notification/i18n"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/internal/templates"
	"7link.com/platform/internal/yunxiao"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"sync/atomic"
	"time"
)

const Name = "notification"

var (
	ErrUnavailable = errors.New("notification unavailable")
	ErrInvalid     = errors.New("notification invalid argument")
	ErrNotFound    = errors.New("notification not found")
	ErrForbidden   = errors.New("notification forbidden")
	ErrConflict    = errors.New("notification version conflict")
)

type Config struct {
	module.Config
	DefaultLocale, EncryptionKey, EncryptionKeyFile string
	PreviousEncryptionKeys                          []string
	Email                                           EmailConfig
}
type Module struct {
	acceptanceReminderFacts  clientwork.AcceptanceReminderFacts
	templateLifecycle        templates.LifecycleFacts
	attentionExecutions      execution.AttentionExecutions
	automationFacts          yunxiao.AutomationNotifications
	smtpFailures             atomic.Uint64
	machineNoticeFacts       fleet.MachineNoticeFacts
	machineOfflineMachines   fleet.MachineOfflineMachines
	enrollmentFacts          fleet.EnrollmentNotificationFacts
	worklogApprovalCount     yunxiao.WorklogApprovalCount
	spawnFacts               governance.SpawnNotificationFacts
	triageReminder           intake.TriageReminderSource
	interactionReminder      execution.InteractionReminderSource
	clarificationFacts       clientwork.ClarificationNotificationFacts
	clarificationReplySource delivery.ClarificationReplySource
	clarificationHandled     delivery.ClarificationHandled
	knowledgeLinks           knowledge.NotificationLink
	intakeTriaged            intake.TriagedNotificationSource
	knowledgeAudience        knowledge.NotificationAudience
	templatePublications     templates.PublishedNotificationFacts
	intakeSource             intake.NotificationSource
	intakeTargets            yunxiao.IntakeNotifyTargets
	planningFacts            PlanningNotifications
	exceptionFacts           clientwork.ExceptionNotificationFacts
	collaborationFacts       clientwork.CollaborationNotificationFacts
	dispatchInitiators       clientwork.DispatchRequestInitiatorFacts
	ensembleFacts            fleet.EnsembleDecisionFacts
	subjectHumanActor        events.SubjectHumanActor
	projectOccurrences       events.ProjectOccurrences
	decisionFacts            delivery.DecisionNotifications
	projectControls          delivery.ProjectControlNotifications
	deliveryConfirmations    delivery.DeliveryConfirmationFacts
	delegationFacts          governance.DelegationNotificationFacts
	incidentFacts            governance.IncidentNotificationFacts
	activity                 agents.ActivityNotifications
	roles                    governance.SeededRoles
	explain                  core.AuthorizationExplanation
	workFacts                delivery.WorkNotifications
	executionFacts           execution.NotificationFactsOn
	interactionFacts         execution.InteractionNotificationOn
	ruleEligible             knowledge.RuleNotificationEligible
	mentions                 collab.MentionNotifications
	broadcast                realtime.Broadcaster
	projectAuthority         delivery.ProjectNotificationAuthority
	rotationRequester        fleet.RotationNotificationRequester
	rotationFacts            fleet.RotationNotificationFacts
	gatewayRotations         fleet.GatewayRotationFacts
	publisher                events.Publisher
	queue                    jobs.Queue
	admission                jobs.QueueAdmission
	guard                    dlock.TransactionGuard
	keyring                  *keyringx.Keyring
	runtime                  *appenv.Runtime
	config                   Config
	names                    query.TableNames
	ids                      guid.Source
	registrar                *authz.Registrar
	principal                identity.PrincipalStateStore
	members                  core.ActiveMemberChecker
	tenantPermission         core.TenantConstraintOn
	platformPermission       core.PlatformPermissionOn
	entityPermission         core.EntityConstraintOn
	candidates               identity.NotificationCandidates
	now                      func() time.Time
}

func NewModule(rt *appenv.Runtime, c Config) *Module {
	return &Module{runtime: rt, config: c, names: query.BindNames(rt.TablePrefix()), now: func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{guid.Name, core.Name, identity.Name, events.Name, jobs.Name, fleet.Name, dlock.Name, delivery.Name, realtime.Name, collab.Name, agents.Name, governance.Name, execution.Name, knowledge.Name, clientwork.Name, intake.Name, yunxiao.Name, templates.Name}
}
func (m *Module) ProvideServices(s *module.Services) error {
	if err := module.Provide[governance.ExecutionAttentionInbox](s, m.attentionInbox); err != nil {
		return err
	}
	if err := m.providePlanningNotifications(s); err != nil {
		return err
	}
	return module.Provide[Notifier](s, m)
}
func (m *Module) InjectServices(s *module.Services) error {
	if err := m.injectIngress(s); err != nil {
		return err
	}
	if err := m.injectAcceptance(s); err != nil {
		return err
	}
	if source, err := module.Resolve[delivery.DeliveryConfirmationFacts](s); err == nil {
		m.deliveryConfirmations = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[delivery.ProjectControlNotifications](s); err == nil {
		m.projectControls = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[fleet.EnsembleDecisionFacts](s); err == nil {
		m.ensembleFacts = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[clientwork.DispatchRequestInitiatorFacts](s); err == nil {
		m.dispatchInitiators = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[templates.LifecycleFacts](s); err == nil {
		m.templateLifecycle = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err := m.injectTemplateLifecycle(s); err != nil {
		return err
	}
	if source, err := module.Resolve[execution.AttentionExecutions](s); err == nil {
		m.attentionExecutions = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[yunxiao.AutomationNotifications](s); err == nil {
		m.automationFacts = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err := m.bindAutomationNotifier(s); err != nil {
		return err
	}
	if err := m.injectToolAuthNotifications(s); err != nil {
		return err
	}
	if err := m.injectWake(s); err != nil {
		return err
	}
	var machineErr error
	if m.machineOfflineMachines, machineErr = module.Resolve[fleet.MachineOfflineMachines](s); machineErr != nil {
		return machineErr
	}
	if m.machineNoticeFacts, machineErr = module.Resolve[fleet.MachineNoticeFacts](s); machineErr != nil {
		return machineErr
	}
	if source, err := module.Resolve[fleet.EnrollmentNotificationFacts](s); err == nil {
		m.enrollmentFacts = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err := m.injectReminder(s); err != nil {
		return err
	}
	if err := m.injectReminderSources(s); err != nil {
		return err
	}
	if source, err := module.Resolve[delivery.ClarificationReplySource](s); err == nil {
		m.clarificationReplySource = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[delivery.ClarificationHandled](s); err == nil {
		m.clarificationHandled = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[clientwork.ClarificationNotificationFacts](s); err == nil {
		m.clarificationFacts = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[knowledge.NotificationLink](s); err == nil {
		m.knowledgeLinks = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[intake.TriagedNotificationSource](s); err == nil {
		m.intakeTriaged = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[knowledge.NotificationAudience](s); err == nil {
		m.knowledgeAudience = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[templates.PublishedNotificationFacts](s); err == nil {
		m.templatePublications = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[intake.NotificationSource](s); err == nil {
		m.intakeSource = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[yunxiao.IntakeNotifyTargets](s); err == nil {
		m.intakeTargets = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[yunxiao.WorklogApprovalCount](s); err == nil {
		m.worklogApprovalCount = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err := m.injectSpawnBudget(s); err != nil {
		return err
	}
	var err error
	if m.collaborationFacts, err = module.Resolve[clientwork.CollaborationNotificationFacts](s); err != nil {
		return err
	}
	if m.subjectHumanActor, err = module.Resolve[events.SubjectHumanActor](s); err != nil {
		return err
	}
	if m.projectOccurrences, err = module.Resolve[events.ProjectOccurrences](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.gatewayRotations, err = module.Resolve[fleet.GatewayRotationFacts](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.exceptionFacts, err = module.Resolve[clientwork.ExceptionNotificationFacts](s); err != nil {
		return err
	}
	if m.decisionFacts, err = module.Resolve[delivery.DecisionNotifications](s); err != nil {
		return err
	}
	if source, e := module.Resolve[governance.IncidentNotificationFacts](s); e == nil {
		m.incidentFacts = source
	} else if !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.delegationFacts, err = module.Resolve[governance.DelegationNotificationFacts](s); err != nil {
		return err
	}
	if m.ruleEligible, err = module.Resolve[knowledge.RuleNotificationEligible](s); err != nil {
		return err
	}
	if source, e := module.Resolve[execution.InteractionNotificationOn](s); e == nil {
		m.interactionFacts = source
	} else if !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.executionFacts, err = module.Resolve[execution.NotificationFactsOn](s); err != nil {
		return err
	}
	if m.roles, err = module.Resolve[governance.SeededRoles](s); err != nil {
		return err
	}
	if m.explain, err = module.Resolve[core.AuthorizationExplanation](s); err != nil {
		return err
	}
	if m.workFacts, err = module.Resolve[delivery.WorkNotifications](s); err != nil {
		return err
	}
	if m.activity, err = module.Resolve[agents.ActivityNotifications](s); err != nil {
		return err
	}
	if m.activity.Bind == nil || m.activity.SnapshotOn == nil {
		return ErrUnavailable
	}
	if err = m.activity.Bind(m.activityChangedOn); err != nil {
		return err
	}
	if m.mentions, err = module.Resolve[collab.MentionNotifications](s); err != nil {
		return err
	}
	if m.broadcast, err = module.Resolve[realtime.Broadcaster](s); err != nil {
		return err
	}
	if m.projectAuthority, err = module.Resolve[delivery.ProjectNotificationAuthority](s); err != nil {
		return err
	}
	if m.admission, err = module.Resolve[jobs.QueueAdmission](s); err != nil {
		return err
	}
	if m.guard, err = module.Resolve[dlock.TransactionGuard](s); err != nil {
		return err
	}
	if m.publisher, err = module.Resolve[events.Publisher](s); err != nil {
		return err
	}
	if m.queue, err = module.Resolve[jobs.Queue](s); err != nil {
		return err
	}
	if m.ids, err = module.Resolve[guid.Source](s); err != nil {
		return err
	}
	if m.registrar, err = module.Resolve[*authz.Registrar](s); err != nil {
		return err
	}
	if m.principal, err = module.Resolve[identity.PrincipalStateStore](s); err != nil {
		return err
	}
	if m.members, err = module.Resolve[core.ActiveMemberChecker](s); err != nil {
		return err
	}
	if m.platformPermission, err = module.Resolve[core.PlatformPermissionOn](s); err != nil {
		return err
	}
	if m.tenantPermission, err = module.Resolve[core.TenantConstraintOn](s); err != nil {
		return err
	}
	if m.entityPermission, err = module.Resolve[core.EntityConstraintOn](s); err != nil {
		return err
	}
	if m.candidates, err = module.Resolve[identity.NotificationCandidates](s); err != nil {
		return err
	}
	if m.rotationFacts, err = module.Resolve[fleet.RotationNotificationFacts](s); err != nil {
		return err
	}
	if m.rotationRequester, err = module.Resolve[fleet.RotationNotificationRequester](s); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.Catalog](s)
	if err != nil {
		return err
	}
	if err = catalog.AddTypes(Name, events.TypeSpec{DisplayNameKey: notificationi18n.Namespace + ".activity_event_notification_delivery_state_changed_name", DescriptionKey: notificationi18n.Namespace + ".activity_event_notification_delivery_state_changed_description", Name: "notification.delivery_state_changed", SchemaVersion: 1, SubjectKind: "notification_delivery", Realtime: events.RealtimeNone}); err != nil {
		return err
	}
	subscribers, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	if err = subscribers.AddSubscribers(Name, m.subscriber(), m.messageEditedSubscriber()); err != nil {
		return err
	}
	parked, err := module.Resolve[events.ParkedNotifications](s)
	if err != nil {
		return err
	}
	if parked.Bind == nil {
		return ErrUnavailable
	}
	return parked.Bind(m.parkedNotificationOn)
}
func (m *Module) Initialize(context.Context) error {
	if m.ids == nil || m.registrar == nil || m.principal.ActiveHumanByID == nil || m.members == nil || m.candidates == nil || m.tenantPermission == nil || m.entityPermission == nil {
		return ErrUnavailable
	}
	if err := m.configureEmail(); err != nil {
		return err
	}
	return notificationi18n.Register()
}
func (m *Module) writer() (*gorm.DB, error) {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil {
		return nil, ErrUnavailable
	}
	db := m.runtime.Database().Write()
	if db == nil {
		return nil, ErrUnavailable
	}
	return db, nil
}
func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{{DisplayNameKey: notificationi18n.Namespace + ".activity_job_notification_accepted_summary_name", DescriptionKey: notificationi18n.Namespace + ".activity_job_notification_accepted_summary_description", Kind: Name + ".accepted_summary", Queue: "notifications", MaxAttempts: 8, BackoffBase: time.Minute, Run: m.acceptedSummaryJob}, {DisplayNameKey: notificationi18n.Namespace + ".activity_job_notification_machine_offline_scan_name", DescriptionKey: notificationi18n.Namespace + ".activity_job_notification_machine_offline_scan_description", Kind: Name + ".machine_offline_scan", Queue: "notifications", MaxAttempts: 8, BackoffBase: time.Minute, Run: m.machineOfflineScanJob}, {DisplayNameKey: notificationi18n.Namespace + ".activity_job_notification_machine_offline_name", DescriptionKey: notificationi18n.Namespace + ".activity_job_notification_machine_offline_description", Kind: Name + ".machine_offline", Queue: "notifications", MaxAttempts: 8, BackoffBase: time.Minute, Run: m.machineOfflineJob}, {DisplayNameKey: notificationi18n.Namespace + ".activity_job_notification_enrollment_warning_name", DescriptionKey: notificationi18n.Namespace + ".activity_job_notification_enrollment_warning_description", Kind: Name + ".enrollment_warning", Queue: "notifications", MaxAttempts: 8, BackoffBase: time.Minute, Run: m.enrollmentWarningJob}, {DisplayNameKey: notificationi18n.Namespace + ".activity_job_notification_stage_suggestions_name", DescriptionKey: notificationi18n.Namespace + ".activity_job_notification_stage_suggestions_description", Kind: Name + ".stage_suggestions", Queue: "notifications", MaxAttempts: 8, BackoffBase: time.Minute, Run: m.stageSuggestionJob}, {DisplayNameKey: notificationi18n.Namespace + ".activity_job_notification_worklog_weekly_name", DescriptionKey: notificationi18n.Namespace + ".activity_job_notification_worklog_weekly_description", Kind: Name + ".worklog_weekly", Queue: "notifications", MaxAttempts: 8, BackoffBase: time.Minute, Run: m.worklogWeeklyJob}, {DisplayNameKey: notificationi18n.Namespace + ".activity_job_notification_source_reminder_name", DescriptionKey: notificationi18n.Namespace + ".activity_job_notification_source_reminder_description", Kind: Name + ".source_reminder", Queue: "notifications", MaxAttempts: 8, BackoffBase: time.Minute, Run: m.sourceReminderJob}, {DisplayNameKey: notificationi18n.Namespace + ".activity_job_notification_delegation_warning_name", DescriptionKey: notificationi18n.Namespace + ".activity_job_notification_delegation_warning_description", Kind: Name + ".delegation_warning", Queue: "notifications", MaxAttempts: 8, BackoffBase: time.Minute, Run: m.delegationWarningJob}, {DisplayNameKey: notificationi18n.Namespace + ".activity_job_notification_deliver_name", DescriptionKey: notificationi18n.Namespace + ".activity_job_notification_deliver_description", Kind: Name + ".deliver", Queue: "notifications", MaxAttempts: 8, BackoffBase: 30 * time.Second, Run: m.deliverJob}, {DisplayNameKey: notificationi18n.Namespace + ".activity_job_notification_rotation_warning_name", DescriptionKey: notificationi18n.Namespace + ".activity_job_notification_rotation_warning_description", Kind: Name + ".rotation_warning", Queue: "notifications", MaxAttempts: 8, BackoffBase: time.Minute, Run: m.rotationWarningJob}, {DisplayNameKey: notificationi18n.Namespace + ".activity_job_notification_agent_error_name", DescriptionKey: notificationi18n.Namespace + ".activity_job_notification_agent_error_description", Kind: Name + ".agent_error", Queue: "notifications", MaxAttempts: 8, BackoffBase: time.Minute, Run: m.agentErrorJob}}
}

var (
	_ module.Depender                = (*Module)(nil)
	_ module.Enabler                 = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
	_ module.HTTPRegistrar           = (*Module)(nil)
	_ jobs.HandlerProvider           = (*Module)(nil)
	_ Notifier                       = (*Module)(nil)
)
