// Package execution owns fixed execution plans and fenced runner receipts.
package execution

//go:generate go run ./dbgen

import (
	"7link.com/platform/internal/delivery"
	executioni18n "7link.com/platform/internal/execution/i18n"
	"7link.com/platform/internal/execution/query"
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
	"7link.com/platform/internal/yunxiao"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
)

const Name = "execution"
const StateCanceled = "CANCELLED" //nolint:misspell // Protocol spelling.
const DeliveryBridgeName = "execution_delivery"

var (
	ErrUnavailable           = errors.New("execution: unavailable")
	ErrCapabilityUnavailable = errors.New("execution: delivery capability unavailable")
	ErrInvalidArgument       = errors.New("execution: invalid argument")
	ErrForbidden             = errors.New("execution: forbidden")
	ErrNotFound              = errors.New("execution: not found")
	ErrFenced                = errors.New("execution: runner message fenced")
	ErrConflict              = errors.New("execution: conflict")
	ErrApprovalUnavailable   = errors.New("execution: requested authorization mode requires an unavailable Human approval bridge")
)

type Config struct {
	module.Config
	Terminal struct {
		RecordingRetentionDays int `mapstructure:"recording_retention_days"`
	} `mapstructure:"terminal"`
}
type Module struct {
	planning           delivery.PlanningWork
	mergeGuard         delivery.CodeMergeGuard
	clientThreads      clientThreadServices
	handover           handoverServices
	verifierRegistry   verifierRegistryServices
	preview            previewServices
	commentIdempotency idempotency.Store
	commentFiles       files.PinnedResources
	commentMembers     core.ActiveMemberChecker
	commentActivities  delivery.WorkItemActivityRecorder
	commentContext     delivery.WorkItemActivityReader
	commentFeedback    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (string, error)
	templateVerifiers  templateVerifierServices
	reauth             authn.ReauthenticationConsumer
	knowledge          knowledgeRuntime
	runtime            *appenv.Runtime
	config             Config
	names              query.TableNames
	ids                guid.Source
	machines           fleet.MachineDirectory
	agents             fleet.AgentDirectory
	agentRuntime       fleet.AgentRuntimeFacts
	principals         identity.PrincipalStateStore
	policy             core.EntityConstraintOn
	registrar          *authz.Registrar
	audit              audit.Appender
	events             events.Publisher
	work               WorkItems
	artifacts          artifactFiles
}

func NewModule(rt *appenv.Runtime, c Config) *Module {
	return &Module{runtime: rt, config: c, names: query.BindNames(rt.TablePrefix())}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return append(append(knowledgeRuntimeDependencies(), templateVerifierDependencies()...), []string{idempotency.Name, iamauthn.Name, yunxiao.Name, fleet.Name, files.Name, identity.Name, core.Name, guid.Name, audit.Name, events.Name, jobs.Name, platformi18n.Name, DeliveryBridgeName}...)
}
func (m *Module) InjectServices(s *module.Services) error {
	if err := m.bindMachineHistory(s); err != nil {
		return err
	}
	if err := m.bindAttentionInteraction(s); err != nil {
		return err
	}
	if err := m.bindMetrics(s); err != nil {
		return err
	}
	if err := m.injectCodeMerge(s); err != nil {
		return err
	}
	if err := m.bindAgentPerformance(s); err != nil {
		return err
	}
	if err := m.bindWorkdirLifecycle(s); err != nil {
		return err
	}
	if err := m.bindProjectStageExecutions(s); err != nil {
		return err
	}
	if err := m.injectArchiveCalibration(s); err != nil {
		return err
	}
	if err := m.bindPreview(s); err != nil {
		return err
	}
	if err := m.bindTerminalExecutions(s); err != nil {
		return err
	}
	if err := m.injectArtifactComments(s); err != nil {
		return err
	}
	if err := m.bindAcceptanceExecutions(s); err != nil {
		return err
	}
	if err := m.injectTemplateVerifiers(s); err != nil {
		return err
	}
	if err := m.bindAgentRebind(s); err != nil {
		return err
	}
	if err := m.injectKnowledgeRuntime(s); err != nil {
		return err
	}
	if err := m.bindMemory(s); err != nil {
		return err
	}
	if err := m.bindSpawnCosts(s); err != nil {
		return err
	}
	var err error
	if m.reauth, err = module.Resolve[authn.ReauthenticationConsumer](s); err != nil {
		return err
	}
	if m.ids, err = module.Resolve[guid.Source](s); err != nil {
		return err
	}
	if m.machines, err = module.Resolve[fleet.MachineDirectory](s); err != nil {
		return err
	}
	if m.agents, err = module.Resolve[fleet.AgentDirectory](s); err != nil {
		return err
	}
	if m.agentRuntime, err = module.Resolve[fleet.AgentRuntimeFacts](s); err != nil {
		return err
	}
	if m.principals, err = module.Resolve[identity.PrincipalStateStore](s); err != nil {
		return err
	}
	if m.policy, err = module.Resolve[core.EntityConstraintOn](s); err != nil {
		return err
	}
	if m.registrar, err = module.Resolve[*authz.Registrar](s); err != nil {
		return err
	}
	if m.audit, err = module.Resolve[audit.Appender](s); err != nil {
		return err
	}
	if m.events, err = module.Resolve[events.Publisher](s); err != nil {
		return err
	}
	if m.work, err = module.Resolve[WorkItems](s); err != nil {
		return err
	}
	if err = m.injectArtifactCriteria(s); err != nil {
		return err
	}
	if err = m.injectArtifactFiles(s); err != nil {
		return err
	}
	if err = m.registerInbox(s); err != nil {
		return err
	}
	if err = m.registerInteractionInbox(s); err != nil {
		return err
	}
	if err = m.registrar.Registry().Contribute([]authz.Action{
		{Resource: "code", Verb: "merge", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "integrate accepted verified code"},
		{Resource: "verifier", Verb: "manage", Scope: authz.ActionScopeTenant, Summary: "manage versioned tenant verifiers"},
		{Resource: "interaction", Verb: "request", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "request runtime tool permissions"},
		{Resource: "interaction", Verb: "respond", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "respond to runtime permission requests"},
		{Resource: "terminal", Verb: "interact", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "open recorded interactive terminal"},
		{Resource: "evidence", Verb: "submit", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "submit manual evidence drafts"},
		{Resource: "execution", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "read executions"},
		{Resource: "execution", Verb: "start", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "start executions"},
		{Resource: "execution", Verb: "stop", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "stop executions"},
	}, nil); err != nil {
		return err
	}
	if err := m.bindPlanning(s); err != nil {
		return err
	}
	if err := m.provideExecutionHolds(s); err != nil {
		return err
	}
	if err := m.bindCancellation(s); err != nil {
		return err
	}
	if err := m.bindSkillDistillation(s); err != nil {
		return err
	}
	if err := m.bindVerifierRegistry(s); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.Catalog](s)
	if err != nil {
		return err
	}
	return catalog.AddTypes(Name, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_comment_thread_resolved_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_comment_thread_resolved_description", Name: "execution.comment_thread_resolved", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_comment_thread_reopened_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_comment_thread_reopened_description", Name: "execution.comment_thread_reopened", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_comment_edited_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_comment_edited_description", Name: "execution.comment_edited", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_comment_retracted_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_comment_retracted_description", Name: "execution.comment_retracted", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_comment_anchor_checked_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_comment_anchor_checked_description", Name: "execution.comment_anchor_checked", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_comment_converted_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_comment_converted_description", Name: "execution.comment_converted", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_merge_requested_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_merge_requested_description", Name: "execution.merge_requested", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_merge_finished_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_merge_finished_description", Name: "execution.merge_finished", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_merged_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_merged_description", Name: "execution.merged", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_branch_created_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_branch_created_description", Name: "execution.branch_created", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_verifier_changed_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_verifier_changed_description", Name: "execution.verifier_changed", SchemaVersion: 1, SubjectKind: "verifier", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_preview_changed_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_preview_changed_description", Name: "execution.preview_changed", SchemaVersion: 1, SubjectKind: "preview_session", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_artifact_comment_added_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_artifact_comment_added_description", Name: "execution.artifact_comment_added", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_full_auto_enabled_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_full_auto_enabled_description", Name: "execution.full_auto_enabled", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_verification_queued_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_verification_queued_description", Name: "execution.verification_queued", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_verification_completed_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_verification_completed_description", Name: "execution.verification_completed", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_state_changed_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_state_changed_description", Name: "execution.state_changed", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: executioni18n.Namespace + ".activity_event_execution_evidence_recorded_name", DescriptionKey: executioni18n.Namespace + ".activity_event_execution_evidence_recorded_description", Name: "execution.evidence_recorded", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal})
}
func (m *Module) Initialize(context.Context) error {
	if err := executioni18n.Register(); err != nil {
		return err
	}
	if m.ids == nil || m.policy == nil || m.principals.Lock == nil || m.audit == nil || m.events == nil || m.work.AuthorizeOn == nil || m.work.StartedOn == nil || m.work.FinishedOn == nil || m.work.BudgetOn == nil || m.work.ReserveBudgetOn == nil {
		return ErrUnavailable
	}
	return nil
}

var (
	_ module.Enabler                 = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)
