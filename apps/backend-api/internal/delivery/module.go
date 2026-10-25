// Package delivery owns projects, work items and delivery business records.
package delivery

//go:generate go run ./dbgen

import (
	"context"
	"errors"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"gorm.io/gorm"
	"log/slog"
	"sync"

	"7link.com/platform/internal/collab"
	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/performance"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
)

const Name = "delivery"

type Config struct{ module.Config }

// ProjectProvisioningConnector lets the composition root bind optional
// provisioning capabilities before initialization. Authentication/authorization
// remains a required IAM dependency; unavailable provisioning never bypasses it.
type ProjectProvisioningConnector struct {
	Bind func(core.EntityProvisioner, core.EntityRoleBindingStore, collab.ChannelProvisioner) error
}

// Module owns the delivery schema and business capabilities.
type Module struct {
	defectMerge               DefectMergeReader
	attentionInteraction      AttentionInteraction
	planningRuntime           PlanningRuntime
	workflowSelection         WorkflowSelection
	workflowDisplayKey        func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, string) (string, error)
	metricsSources            DeliveryMetricsSources
	pipelineClient            PipelineClientSource
	stagePackages             ProjectStagePackages
	stageExecutions           func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)
	stageCleanup              governance.ProjectTerminalCleanup
	stageCancellation         *clientWork
	archiveSources            projectArchiveSources
	settingsMembers           core.ProjectMemberBindings
	clientPackageConfirmation ClientPackageConfirmationSource
	autonomyQueue             jobs.Queue
	modeGovernance            governance.ProjectModeGovernance
	ensembleBranchCheck       fleet.EnsembleBranchCheck
	executionHolds            ExecutionHolds
	clarificationReplySource  ClarificationReplySource
	artifactComments          ArtifactComments
	acceptanceExecutions      AcceptanceExecutionIDs
	acceptanceSettlements     AcceptanceSettlements
	knowledgeContext          workKnowledgeResolver
	hiddenVerifier            HiddenAcceptanceVerifier
	requirementCases          RequirementCaseReader
	verificationExecutor      VerificationExecutor
	runtime                   *appenv.Runtime
	config                    Config
	service                   *projectService
	ids                       guid.Source
	roles                     governance.SeededRoles
	idempotency               idempotency.Store
	createAuthority           governance.ProjectCreationAuthority
	stageAuthority            governance.ProjectStageAuthority
	deliveryAuthority         governance.DeliveryAuthority
	andonAuthority            governance.AndonAuthority
	scopeReauthentication     authnext.ReauthenticationConsumer
	resultFiles               files.PinnedResources
	assignmentGuard           dlock.TransactionGuard
	leaseFailures             prometheus.Counter
	leaseFailureMu            sync.Mutex
	leaseFailureRounds        map[guid.ID]int
	members                   core.ActiveMemberChecker
	agents                    fleet.AgentDirectory
	triageAgents              fleet.SpawnGrantDirectory
	reviewTypePolicy          core.TenantConstraintOn
	delegatedNewSources       intake.DelegatedNewSources
	sources                   intake.IntakeReader
	principals                identity.PrincipalStateStore
	registrar                 *authz.Registrar
	checker                   core.AuthorizationEvaluator
	entityOwner               core.EntityOwnerResolver
	projectAccess             core.AccessTableNames
	listConstraint            core.TenantConstraint
	workPolicy                core.EntityConstraintOn
	provisioningBound         bool
	initialized               bool
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	return &Module{runtime: rt, config: config, service: &projectService{names: query.BindNames(rt.TablePrefix())}, leaseFailures: prometheus.NewCounter(prometheus.CounterOpts{Name: "delivery_lease_reclaim_failures_total", Help: "Failed Human assignment lease recovery attempts."})}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{performance.Name, knowledge.Name, platformi18n.Name, guid.Name, idempotency.Name, core.Name, identity.Name, fleet.Name, intake.Name, audit.Name, events.Name, governance.Name, jobs.Name, dlock.Name, files.Name, iamauthn.Name}
}
func (m *Module) ProvideServices(services *module.Services) error {
	if err := m.provideAttentionInteraction(services); err != nil {
		return err
	}
	if err := m.providePlanningRuntime(services); err != nil {
		return err
	}
	if err := m.providePackageInputs(services); err != nil {
		return err
	}
	if err := m.provideReplyRouting(services); err != nil {
		return err
	}
	if err := m.provideAgentPerformanceLoad(services); err != nil {
		return err
	}
	if err := m.provideRetirementKnowledge(services); err != nil {
		return err
	}
	if err := m.providePipeline(services); err != nil {
		return err
	}
	if e := m.provideStageTerminal(services); e != nil {
		return e
	}
	if err := m.provideArchiveReviews(services); err != nil {
		return err
	}
	if err := m.provideProjectBudgetHistory(services); err != nil {
		return err
	}
	if err := m.provideAcceptanceReminderProjects(services); err != nil {
		return err
	}
	if err := m.provideReminderWork(services); err != nil {
		return err
	}
	if err := m.provideDeliveryConfirmations(services); err != nil {
		return err
	}
	if err := m.provideProjectControls(services); err != nil {
		return err
	}
	if err := m.provideProjectModes(services); err != nil {
		return err
	}
	if err := m.provideExecutionHolds(services); err != nil {
		return err
	}
	if err := m.provideClarificationReceipts(services); err != nil {
		return err
	}
	if err := m.provideVerifierWorkPolicy(services); err != nil {
		return err
	}
	if err := m.provideArtifactComments(services); err != nil {
		return err
	}
	if err := m.provideAcceptanceCost(services); err != nil {
		return err
	}
	if err := m.provideReportQuality(services); err != nil {
		return err
	}
	if err := m.provideWorkActivity(services); err != nil {
		return err
	}
	if err := m.provideDispatchHistory(services); err != nil {
		return err
	}
	if err := m.provideHiddenAcceptance(services); err != nil {
		return err
	}
	if err := m.provideKnowledgeContext(services); err != nil {
		return err
	}
	if err := module.Provide[RequirementSuiteConnector](services, RequirementSuiteConnector{Bind: func(reader RequirementCaseReader) { m.requirementCases = reader }}); err != nil {
		return err
	}
	if err := m.provideNotificationFacts(services); err != nil {
		return err
	}
	if err := m.provideWorkflowStates(services); err != nil {
		return err
	}
	if err := m.provideWorkflowGovernance(services); err != nil {
		return err
	}
	if err := m.provideHLoopAssignments(services); err != nil {
		return err
	}
	if err := m.provideHPlanning(services); err != nil {
		return err
	}
	if err := m.provideVerification(services); err != nil {
		return err
	}
	if err := m.provideAcceptanceContacts(services); err != nil {
		return err
	}
	if err := m.provideClientWork(services); err != nil {
		return err
	}
	if err := m.provideExecution(services); err != nil {
		return err
	}
	if err := m.provideJArtifactCriteria(services); err != nil {
		return err
	}
	if err := m.providePlanningTemplates(services); err != nil {
		return err
	}
	if err := m.provideFleetRepository(services); err != nil {
		return err
	}
	if err := m.provideCodeLinks(services); err != nil {
		return err
	}
	if err := module.Provide[core.EntityTypeReservations](services, func() []string { return []string{"project"} }); err != nil {
		return err
	}
	return module.Provide[ProjectProvisioningConnector](services, ProjectProvisioningConnector{Bind: m.bindProvisioning})
}
func (m *Module) bindProvisioning(entities core.EntityProvisioner, roles core.EntityRoleBindingStore, channels collab.ChannelProvisioner) error {
	if m == nil || m.service == nil {
		return ErrUnavailable
	}
	if m.provisioningBound || m.initialized {
		return ErrInvalidArgument
	}
	m.service.entities, m.service.roles, m.service.channels = entities, roles, channels
	m.provisioningBound = true
	return nil
}
func (m *Module) InjectServices(services *module.Services) error {
	if e := m.injectStageTerminal(services); e != nil {
		return e
	}
	if err := m.injectArchiveReviews(services); err != nil {
		return err
	}
	if err := m.injectSettingsMembers(services); err != nil {
		return err
	}
	if err := m.bindKnowledgeEventReader(services); err != nil {
		return err
	}
	if err := m.bindKnowledgeResults(services); err != nil {
		return err
	}
	if m == nil || m.service == nil {
		return ErrUnavailable
	}
	if err := m.injectDelegatedNew(services); err != nil {
		return err
	}
	if err := m.injectMerge(services); err != nil {
		return err
	}
	if err := m.injectDeliveryConfirmations(services); err != nil {
		return err
	}
	if err := m.injectModes(services); err != nil {
		return err
	}
	if err := m.injectAutonomyWatch(services); err != nil {
		return err
	}
	if source, err := module.Resolve[ClarificationReplySource](services); err == nil {
		m.clarificationReplySource = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[governance.AndonAuthority](services); err == nil {
		m.andonAuthority = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err := m.bindAutomatedDecisions(services); err != nil {
		return err
	}
	if err := m.injectKnowledgeContext(services); err != nil {
		return err
	}

	if err := m.bindDelegationProjects(services); err != nil {
		return err
	}
	if err := m.injectWork(services); err != nil {
		return err
	}
	var err error
	if m.deliveryAuthority, err = module.Resolve[governance.DeliveryAuthority](services); err != nil {
		return err
	}
	if m.scopeReauthentication, err = module.Resolve[authnext.ReauthenticationConsumer](services); err != nil {
		return err
	}
	if m.resultFiles, err = module.Resolve[files.PinnedResources](services); err != nil {
		return err
	}
	bindings, err := module.Resolve[*files.ResourceBindings](services)
	if err != nil {
		return err
	}
	if err = bindings.RegisterPurpose(files.ResourcePurpose{Name: resultEvidencePurpose, ResourceKind: "delivery_result", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: m.executionWorkAuthorizeResultResource}); err != nil {
		return err
	}
	if m.assignmentGuard, err = module.Resolve[dlock.TransactionGuard](services); err != nil {
		return err
	}
	if m.ids, err = module.Resolve[guid.Source](services); err != nil {
		return err
	}
	if m.idempotency, err = module.Resolve[idempotency.Store](services); err != nil {
		return err
	}
	if m.roles, err = module.Resolve[governance.SeededRoles](services); err != nil {
		return err
	}
	if m.createAuthority, err = module.Resolve[governance.ProjectCreationAuthority](services); err != nil {
		return err
	}
	if m.stageAuthority, err = module.Resolve[governance.ProjectStageAuthority](services); err != nil {
		return err
	}
	if m.sources, err = module.Resolve[intake.IntakeReader](services); err != nil {
		return err
	}
	if m.agents, err = module.Resolve[fleet.AgentDirectory](services); err != nil {
		return err
	}
	if m.triageAgents, err = module.Resolve[fleet.SpawnGrantDirectory](services); err != nil {
		return err
	}
	if m.reviewTypePolicy, err = module.Resolve[core.TenantConstraintOn](services); err != nil {
		return err
	}
	if m.projectAccess, err = module.Resolve[core.AccessTableNames](services); err != nil {
		return err
	}
	if m.listConstraint, err = module.Resolve[core.TenantConstraint](services); err != nil {
		return err
	}
	if m.workPolicy, err = module.Resolve[core.EntityConstraintOn](services); err != nil {
		return err
	}
	if m.entityOwner, err = module.Resolve[core.EntityOwnerResolver](services); err != nil {
		return err
	}
	if m.members, err = module.Resolve[core.ActiveMemberChecker](services); err != nil {
		return err
	}
	if m.principals, err = module.Resolve[identity.PrincipalStateStore](services); err != nil {
		return err
	}
	if m.registrar, err = module.Resolve[*authz.Registrar](services); err != nil {
		return err
	}
	if m.registrar == nil || m.registrar.Registry() == nil {
		return ErrUnavailable
	}
	if err = m.registrar.Registry().Contribute([]authz.Action{
		{Resource: "verification", Verb: "run", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "run exact-result verification"},
		{Resource: "decision", Verb: "force_valid", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "record a Human verification exception"},
		{Resource: "artifact_comment", Verb: "write", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "comment on exact result evidence"},
		{Resource: "scope", Verb: "change", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "change requirement scope"},
		{Resource: "decision", Verb: "approve", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "accept exact work results"},
		{Resource: "decision", Verb: "request_changes", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "return work results for rework"},
		{Resource: "delivery", Verb: "send", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "handoff and receive delivery packages"},
		{Resource: "requirement", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "read requirements"},
		{Resource: "requirement", Verb: "write", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "create and baseline requirements"},
		{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "read work items"},
		{Resource: "work_item", Verb: "execute", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "execute assigned work items"},
		{Resource: "work_item", Verb: "write", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "create work items"},
		{Resource: "andon", Verb: "pull", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "pull a work Andon hold"},
		{Resource: "andon", Verb: "clear", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "clear a work Andon hold as a Human"},
		{Resource: "plan", Verb: "approve", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "approve or reject work plans"},
		{Resource: "project", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "read projects"},
		{Resource: "project", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "create and manage projects"},
		{Resource: "project", Verb: "stage_change", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "change project stage"},
		{Resource: "review", Verb: "initiate", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Initiate requirement reviews"},
		{Resource: "review", Verb: "participate", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Submit requirement review opinions"},
		{Resource: "project", Verb: "mode_change", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "change project team mode"},
		{Resource: "project", Verb: "takeover", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Take over project or work-item Agent authority"},
	}, nil); err != nil {
		return fmt.Errorf("register project permissions: %w", err)
	}
	if m.checker, err = module.Resolve[core.AuthorizationEvaluator](services); err != nil {
		return err
	}
	if m.service.audit, err = module.Resolve[audit.Appender](services); err != nil {
		return err
	}
	if m.service.events, err = module.Resolve[events.Publisher](services); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.Catalog](services)
	if err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "automation.verification_requested", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "delivery.confirmation_requested", SchemaVersion: 1, SubjectKind: "delivery_confirmation", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "delivery.withdrawn", SchemaVersion: 1, SubjectKind: "delivery_confirmation", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "work_item.requirement_conflict_resolved", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "project.mode_changed", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "project.autonomy_lost", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "project.autonomy_restored", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "project.taken_over", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "clarification.handled", SchemaVersion: 1, SubjectKind: "clarification", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "andon.pulled", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "andon.cleared", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "project.andon_pulled", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "project.andon_cleared", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	for _, event := range []string{"project.constitution_proposed", "project.constitution_submitted", "project.constitution_activated", "project.constitution_rejected"} {
		if err := catalog.AddTypes(Name, events.TypeSpec{Name: event, SchemaVersion: 1, SubjectKind: "project_constitution", Realtime: events.RealtimeSignal}); err != nil {
			return err
		}
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "project.members_changed", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "project.stage_changed", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "project.stage_ready", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "result.feedback_added", SchemaVersion: 1, SubjectKind: "result", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "work_item.escalated", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "code_link.created", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "requirement.code_link_created", SchemaVersion: 1, SubjectKind: "requirement", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "requirement.scope_changed", SchemaVersion: 1, SubjectKind: "requirement", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	if err := m.registerInbox(services); err != nil {
		return err
	}
	if err := m.registerReviewSheetInbox(services); err != nil {
		return err
	}
	if err := m.registerRequirementConflictInbox(services); err != nil {
		return err
	}
	return catalog.AddTypes(Name, events.TypeSpec{Name: "work_item.transitioned", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "work_item.attention_decided", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeNone}, events.TypeSpec{Name: "work_item.attention_started", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeNone}, events.TypeSpec{Name: "work_item.attention_updated", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeNone}, events.TypeSpec{Name: "work_item.attention_closed", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeNone}, events.TypeSpec{Name: "work_item.defect_details_updated", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "work_item.schedule_changed", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "work_item.upstream_changed", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "review_type.configured", SchemaVersion: 1, SubjectKind: "review_type", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "review_sheet.opened", SchemaVersion: 1, SubjectKind: "review_sheet", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "review_sheet.closed", SchemaVersion: 1, SubjectKind: "review_sheet", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "review_sheet.opinion_saved", SchemaVersion: 1, SubjectKind: "review_sheet", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "quality.escaped", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "quality.escape_learning_proposed", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "plan.proposed", SchemaVersion: 1, SubjectKind: "plan", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "plan.approved", SchemaVersion: 1, SubjectKind: "plan", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "plan.rejected", SchemaVersion: 1, SubjectKind: "plan", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "verification.completed", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "verification.overridden", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "project.budget_changed", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "budget.threshold_reached", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "work_item.dependency_changed", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "work_item.assigned", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "work_item.ready_checked", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "work_item.state_changed", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "project.created", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "work_item.created", SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "requirement.created", SchemaVersion: 1, SubjectKind: "requirement", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "requirement.updated", SchemaVersion: 1, SubjectKind: "requirement", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "requirement.versioned", SchemaVersion: 1, SubjectKind: "requirement", Realtime: events.RealtimeSignal})
}
func (m *Module) Initialize(ctx context.Context) error {
	if m == nil || m.service == nil || m.ids == nil || m.projectAccess.Entities.IsZero() || m.listConstraint == nil || m.entityOwner == nil || m.idempotency == nil || m.createAuthority == nil || m.principals.Lock == nil || m.checker == nil || m.service.audit == nil || m.service.events == nil || m.roles.EnsureTenant == nil || m.roles.RoleIDOn == nil || m.members == nil || m.principals.ActiveHumanByID == nil || m.agents.ByPrincipalOn == nil || m.sources.GetOn == nil || m.sources.ContactsOn == nil {
		return ErrUnavailable
	}
	if err := deliveryi18n.Register(); err != nil {
		return err
	}
	for name, missing := range map[string]bool{
		"core.EntityProvisioner":      m.service.entities.CreateOn == nil || m.service.entities.GetOn == nil,
		"core.EntityRoleBindingStore": m.service.roles.PutOn == nil,
		"collab.ChannelProvisioner":   m.service.channels.CreateDefaultOn == nil,
	} {
		if missing {
			slog.WarnContext(ctx, "project creation unavailable; enable and wire provisioning provider", "capability", name)
		}
	}
	m.initialized = true
	return nil
}

var (
	_ module.HTTPRegistrar           = (*Module)(nil)
	_ module.Enabler                 = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)
