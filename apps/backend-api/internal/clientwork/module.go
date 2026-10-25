// Package clientwork owns project client collaboration and quality records.
package clientwork

//go:generate go run ./dbgen
import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/budget"
	clienti18n "7link.com/platform/internal/clientwork/i18n"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/appenv"
	authn "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"os/exec"
)

const Name = "clientwork"

type Config struct {
	module.Config
	HiddenVerifier    HiddenVerifierConfig
	SubmissionBundles SubmissionBundleConfig
}
type Module struct {
	submissionEvents intake.SubmissionEvents
	testDefects      delivery.TestFailureDefects
	packageRecipient delivery.PackageDeliveryRecipient
	packageSender    collab.PackageDeliverySender

	packageComments        execution.ClientArtifactComments
	packageCommentContacts delivery.ClientCommentContacts
	acceptanceCalendar     AcceptanceCalendar
	acceptanceProjects     delivery.AcceptanceReminderProjects
	releaseDrafters        delivery.ReleaseNoteDrafters
	recordedContact        delivery.RecordedAcceptanceContact
	acceptanceProof        authn.ReauthenticationConsumer
	intakeWorkOwner        delivery.IntakeWorkOwner
	intakeWorkFence        delivery.IntakeWorkFence
	triageInbox            agents.InboxAppender
	triageIdentity         fleet.SpawnGrantDirectory
	triageTarget           intake.TriageWakeTarget
	intakeConversation     collab.IntakeConversation
	agentMerge             intake.AgentMerge
	dispositionReceipts    collab.IntakeDispositionReceipts
	dispositionNotice      collab.IntakeDispositionNotice
	mergedReplyRoutes      intake.MergedReplyRoutes
	intakeReplyRouting     collab.IntakeReplyRouting
	intakeReplyFence       delivery.IntakeReplyRouteFence
	dispatchStarvation     delivery.DispatchStarvationFacts
	reworkDisposition      delivery.RejectedWorkDisposition
	questionDrafts         delivery.ClarificationDraftAccess
	clientSigners          delivery.ClientSignerFacts
	intakeProjects         delivery.IntakeProjectCreation
	dispatchSchedule       delivery.DispatchScheduleFacts
	dispatchCatalog        agents.SkillCatalog
	dispatchExecutions     execution.DispatchHistory
	dispatchDecisions      delivery.DispatchDecisionHistory
	hiddenIsolation        func(*exec.Cmd, HiddenVerifierConfig, string) (func() error, error)
	hiddenRequest          func(context.Context, *gorm.DB, delivery.HiddenAcceptanceRequest) (delivery.HiddenAcceptanceRequest, error)
	hiddenSource           execution.HiddenSourceFacts
	hiddenService          identity.ServiceAccountValidator
	hiddenPermission       core.EntityConstraintOn
	forecastWork           delivery.ForecastWorkFacts
	forecastExecutions     execution.ForecastFacts
	forecastCosts          budget.SettlementFacts
	forecastQueue          jobs.Queue
	intakeSources          intake.ChangeSources
	reworkSources          intake.ReworkSources
	packageSources         delivery.PackageSourceFacts
	delegatedConversion    delivery.IntakeDelegatedConversion
	intakeAppend           delivery.IntakeAppender
	dispatch               delivery.DispatchAccess
	dispatchFleet          fleet.DispatchFleetFacts
	runtime                *appenv.Runtime
	config                 Config
	names                  query.TableNames
	ids                    guid.Source
	replay                 idempotency.Store
	audit                  audit.Appender
	events                 events.Publisher
	principals             identity.PrincipalStateStore
	members                core.ActiveMemberChecker
	registrar              *authz.Registrar
	verifier               authz.TokenVerifier
	owner                  core.EntityOwnerResolver
	projects               delivery.ClientWorkAccess
	clients                collab.ClientProjectAccess
	bundleInputs           delivery.PackageInputs
	bundleScanner          execution.PackageEvidenceScanner
	bundleFiles            files.DeliveryBundleFiles
	files                  files.ClientCollaborationResources
	answerFiles            files.PinnedResources
}

func NewModule(rt *appenv.Runtime, c Config) *Module {
	return &Module{runtime: rt, config: c, names: query.BindNames(rt.TablePrefix())}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{knowledge.Name, agents.Name, budget.Name, execution.Name, intake.Name, jobs.Name, fleet.Name, iamauthn.Name, files.Name, collab.Name, delivery.Name, platformi18n.Name, guid.Name, idempotency.Name, events.Name, audit.Name, core.Name, identity.Name}
}
func (m *Module) InjectServices(s *module.Services) error {
	if e := m.injectPackageBundles(s); e != nil {
		return e
	}
	if recipient, err := module.Resolve[delivery.PackageDeliveryRecipient](s); err == nil {
		m.packageRecipient = recipient
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err := m.bindMetrics(s); err != nil {
		return err
	}
	if sender, err := module.Resolve[collab.PackageDeliverySender](s); err == nil {
		m.packageSender = sender
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err := m.injectDisposition(s); err != nil {
		return err
	}
	if err := m.bindPackageComments(s); err != nil {
		return err
	}
	if err := m.bindPipeline(s); err != nil {
		return err
	}
	if err := m.injectArchiveResources(s); err != nil {
		return err
	}
	if err := m.bindProjectStages(s); err != nil {
		return err
	}
	if source, err := module.Resolve[delivery.AcceptanceReminderProjects](s); err == nil {
		m.acceptanceProjects = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err := m.bindChannelDispatch(s); err != nil {
		return err
	}
	if err := m.bindKnowledgeAcceptance(s); err != nil {
		return err
	}
	if err := m.injectIntakeOperations(s); err != nil {
		return err
	}
	if err := m.injectIntakeWake(s); err != nil {
		return err
	}
	if err := m.injectIntakeConversation(s); err != nil {
		return err
	}
	if err := m.injectDispatchAnalytics(s); err != nil {
		return err
	}
	if err := m.injectHiddenAcceptance(s); err != nil {
		return err
	}
	if err := m.bindRequirementSuites(s); err != nil {
		return err
	}
	if err := m.bindAutomation(s); err != nil {
		return err
	}
	var e error
	if m.reworkSources, e = module.Resolve[intake.ReworkSources](s); e != nil {
		return e
	}
	if m.packageSources, e = module.Resolve[delivery.PackageSourceFacts](s); e != nil {
		return e
	}
	if m.intakeSources, e = module.Resolve[intake.ChangeSources](s); e != nil {
		return e
	}
	if m.submissionEvents, e = module.Resolve[intake.SubmissionEvents](s); e != nil {
		return e
	}
	if m.delegatedConversion, e = module.Resolve[delivery.IntakeDelegatedConversion](s); e != nil {
		return e
	}
	if m.intakeAppend, e = module.Resolve[delivery.IntakeAppender](s); e != nil {
		return e
	}
	if m.dispatch, e = module.Resolve[delivery.DispatchAccess](s); e != nil {
		return e
	}
	if m.dispatchFleet, e = module.Resolve[fleet.DispatchFleetFacts](s); e != nil {
		return e
	}
	if m.verifier, e = module.Resolve[authz.TokenVerifier](s); e != nil {
		return e
	}
	if m.files, e = module.Resolve[files.ClientCollaborationResources](s); e != nil {
		return e
	}
	bindings, e := module.Resolve[*files.ResourceBindings](s)
	if e != nil {
		return e
	}
	if e = bindings.RegisterPurpose(files.ResourcePurpose{Name: previewPurpose, ResourceKind: "client_preview", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: m.previewResourceOn}); e != nil {
		return e
	}
	if e = bindings.RegisterPurpose(files.ResourcePurpose{Name: packagePurpose, ResourceKind: "client_package", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: m.packageResourceOn}); e != nil {
		return e
	}
	if e = bindings.RegisterPurpose(files.ResourcePurpose{Name: sharePurpose, ResourceKind: "preview_share", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: m.shareResourceOn}); e != nil {
		return e
	}
	if m.projects, e = module.Resolve[delivery.ClientWorkAccess](s); e != nil {
		return e
	}
	if m.clients, e = module.Resolve[collab.ClientProjectAccess](s); e != nil {
		return e
	}
	if m.ids, e = module.Resolve[guid.Source](s); e != nil {
		return e
	}
	if m.replay, e = module.Resolve[idempotency.Store](s); e != nil {
		return e
	}
	if m.audit, e = module.Resolve[audit.Appender](s); e != nil {
		return e
	}
	if m.events, e = module.Resolve[events.Publisher](s); e != nil {
		return e
	}
	if m.principals, e = module.Resolve[identity.PrincipalStateStore](s); e != nil {
		return e
	}
	if m.members, e = module.Resolve[core.ActiveMemberChecker](s); e != nil {
		return e
	}
	if m.registrar, e = module.Resolve[*authz.Registrar](s); e != nil {
		return e
	}
	if e = m.registrar.Registry().Contribute([]authz.Action{{Resource: "intake", Verb: "triage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "request project Intake triage"}, {Resource: "test_case", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "manage project test cases"}, {Resource: "test_plan", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "manage project test plans"}, {Resource: "test_run", Verb: "execute", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "record project test runs"}, {Resource: "acceptance_suite", Verb: "verify", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "execute private acceptance suites as the dedicated verifier service"}, {Resource: "acceptance_suite", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "manage confidential acceptance suites as an authorized Human"}, {Resource: "delivery", Verb: "compose", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "compose project delivery packages"}, {Resource: "work_item", Verb: "assign", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "assign project work"}, {Resource: "exception", Verb: "decide", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "decide a reported tolerance exception"}}, nil); e != nil {
		return e
	}
	m.owner, e = module.Resolve[core.EntityOwnerResolver](s)
	if e == nil {
		e = m.registerNotificationFacts()
		if e == nil {
			e = m.registerAutomationBridge()
		}
	}
	if e != nil {
		return e
	}
	catalog, e := module.Resolve[events.Catalog](s)
	if e != nil {
		return e
	}
	for _, kind := range []string{"package_bundle_requested", "package_bundle_built", "package_bundle_failed", "package_bundle_stale", "package_sent", "package_send_failed", "acceptance_reminder", "acceptance_overdue", "release_note_requested", "release_note_drafted", "release_note_adopted", "intake_suggested", "intake_thread_linked", "intake_thread_replied", "dispatch_starved", "rework_disposed", "clarification_drafted", "clarification_expired", "clarification_reminder", "hidden_verified", "clarification_requested", "clarification_answered", "sheet_drafted", "sheet_published", "sheet_decided", "change_requested", "change_decided", "issue_created", "issue_updated", "package_drafted", "package_published", "package_decided", "package_archived", "preview_created", "annotation_created", "test_report_requested", "test_report_built", "test_report_failed", "test_case_written", "test_plan_created", "test_run_recorded", "settings_changed", "tolerance_changed", "tolerance_forecasted", "exception_raised", "exception_reported", "exception_decided", "preview_share_requested", "preview_share_decided", "preview_share_ended", "dispatch_proposed", "dispatch_decided", "dispatch_expired"} {
		if e = catalog.AddTypes(Name, events.TypeSpec{Name: "clientwork." + kind, SchemaVersion: 1, SubjectKind: Name, Realtime: events.RealtimeNone}); e != nil {
			return e
		}
	}
	if e = catalog.AddTypes(Name, events.TypeSpec{Name: "clientwork.clarification_cancelled", SchemaVersion: 1, SubjectKind: Name, Realtime: events.RealtimeNone}); e != nil { //nolint:misspell // Frozen event name.
		return e
	}
	if e = m.injectForecastServices(s); e != nil {
		return e
	}
	if m.answerFiles, e = module.Resolve[files.PinnedResources](s); e != nil {
		return e
	}
	if e = bindings.RegisterPurpose(files.ResourcePurpose{Name: answerAttachmentPurpose, ResourceKind: "clarification_round", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: m.answerAttachmentResourceOn}); e != nil {
		return e
	}
	if e = m.injectRecordedAcceptance(s, bindings); e != nil {
		return e
	}
	return m.injectDispatchScheduling(s)
}
func (m *Module) writer() *gorm.DB {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil {
		return nil
	}
	return m.runtime.Database().Write()
}
func (m *Module) Initialize(context.Context) error { return clienti18n.Register() }

var (
	_ module.Enabler                 = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)
