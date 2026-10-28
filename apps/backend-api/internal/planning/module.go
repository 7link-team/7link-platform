// Package planning owns evidence-backed goals, period plans and reports.
package planning

//go:generate go run ./dbgen

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/notification"
	planningi18n "7link.com/platform/internal/planning/i18n"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/internal/yunxiao"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	pi "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"time"
)

const Name = "planning"

type Config struct{ module.Config }
type Module struct {
	reportSuperior         delivery.ReportSuperior
	reportExperiences      knowledge.ReportExperiences
	notificationCandidates identity.NotificationCandidates
	homeFacts              delivery.HomeFacts
	homeReceipts           clientwork.HomeReceipts
	homeDelegations        governance.AppliedDelegations
	homeCapacity           fleet.WorkbenchCapacity
	qualityBranchBefore    execution.QualityBranchBefore
	collaborationInbox     clientwork.CollaborationInbox
	heavyReference         delivery.HeavyPlanningReferences
	heavyCommands          execution.HeavyCommands
	heavyBudget            budget.Provider
	workbenchMachines      fleet.WorkbenchMachines
	loopRuntime            loopRuntimeServices
	reportNotifier         notification.Notifier
	runtime                *appenv.Runtime
	config                 Config
	names                  query.TableNames
	ids                    guid.Source
	principals             identity.PrincipalStateStore
	members                core.ActiveMemberChecker
	policy                 core.TenantConstraintOn
	entityPolicy           core.EntityConstraintOn
	agents                 fleet.AgentDirectory
	project                delivery.ProjectScope
	reportQuality          delivery.ReportQualityReader
	memberQuality          delivery.ReportMemberQualityReader
	facts                  delivery.PlanningFacts
	scheduledFacts         delivery.ScheduledPlanningFacts
	planningCarryovers     yunxiao.PlanningCarryovers
	runtimeCosts           RuntimeCosts
	executions             execution.PeriodReader
	loopReminders          agents.ReminderLoops
	loopAnchors            collab.ThreadAnchor
	audit                  audit.Appender
	events                 events.Publisher
	replay                 idempotency.Store
	registrar              *authz.Registrar
	now                    func() time.Time
}

func NewModule(rt *appenv.Runtime, c Config) *Module {
	return &Module{runtime: rt, config: c, names: query.BindNames(rt.TablePrefix()), now: func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }}
}

// Initialize loads templates before scheduled notification delivery can run.
func (*Module) Initialize(context.Context) error { return planningi18n.Register() }
func (*Module) ModuleName() string               { return Name }
func (m *Module) Enabled() bool                  { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{knowledge.Name, clientwork.Name, budget.Name, pi.Name, guid.Name, core.Name, identity.Name, fleet.Name, delivery.Name, execution.Name, governance.Name, audit.Name, events.Name, idempotency.Name, agents.Name, collab.Name, notification.Name, jobs.Name, yunxiao.Name}
}
func (m *Module) InjectServices(s *module.Services) error {
	if err := m.injectArchiveResources(s); err != nil {
		return err
	}
	if err := m.bindAcceptanceCalendar(s); err != nil {
		return err
	}
	if err := m.injectReminderCalendar(s); err != nil {
		return err
	}
	if err := m.injectReportLifecycle(s); err != nil {
		return err
	}
	var e error
	if m.reportExperiences, e = module.Resolve[knowledge.ReportExperiences](s); e != nil && !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.homeDelegations, e = module.Resolve[governance.AppliedDelegations](s); e != nil && !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.homeReceipts, e = module.Resolve[clientwork.HomeReceipts](s); e != nil && !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.homeCapacity, e = module.Resolve[fleet.WorkbenchCapacity](s); e != nil && !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.homeFacts, e = module.Resolve[delivery.HomeFacts](s); e != nil && !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.qualityBranchBefore, e = module.Resolve[execution.QualityBranchBefore](s); e != nil && !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.heavyReference, e = module.Resolve[delivery.HeavyPlanningReferences](s); e != nil && !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.heavyCommands, e = module.Resolve[execution.HeavyCommands](s); e != nil && !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.heavyBudget, e = module.Resolve[budget.Provider](s); e != nil && !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.workbenchMachines, e = module.Resolve[fleet.WorkbenchMachines](s); e != nil {
		return e
	}
	if e = m.injectLoopRuntime(s); e != nil {
		return e
	}
	if e = m.injectScheduler(s); e != nil {
		return e
	}
	if m.loopReminders, e = module.Resolve[agents.ReminderLoops](s); e != nil {
		return e
	}
	if m.loopAnchors, e = module.Resolve[collab.ThreadAnchor](s); e != nil {
		return e
	}
	if m.loopReminders.Bind != nil {
		if e = m.loopReminders.Bind(m.openLoopReminderOn); e != nil {
			return e
		}
	}
	if m.ids, e = module.Resolve[guid.Source](s); e != nil {
		return e
	}
	if m.principals, e = module.Resolve[identity.PrincipalStateStore](s); e != nil {
		return e
	}
	if m.members, e = module.Resolve[core.ActiveMemberChecker](s); e != nil {
		return e
	}
	if m.policy, e = module.Resolve[core.TenantConstraintOn](s); e != nil {
		return e
	}
	if m.entityPolicy, e = module.Resolve[core.EntityConstraintOn](s); e != nil {
		return e
	}
	if m.agents, e = module.Resolve[fleet.AgentDirectory](s); e != nil {
		return e
	}
	if m.project, e = module.Resolve[delivery.ProjectScope](s); e != nil {
		return e
	}
	if m.reportQuality, e = module.Resolve[delivery.ReportQualityReader](s); e != nil && !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.memberQuality, e = module.Resolve[delivery.ReportMemberQualityReader](s); e != nil && !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if m.facts, e = module.Resolve[delivery.PlanningFacts](s); e != nil {
		return e
	}
	if e = m.injectReportGeneration(s); e != nil {
		return e
	}
	if e = m.injectAutomationReportWriter(s); e != nil {
		return e
	}
	if m.executions, e = module.Resolve[execution.PeriodReader](s); e != nil {
		return e
	}
	if m.audit, e = module.Resolve[audit.Appender](s); e != nil {
		return e
	}
	if m.events, e = module.Resolve[events.Publisher](s); e != nil {
		return e
	}
	if m.replay, e = module.Resolve[idempotency.Store](s); e != nil {
		return e
	}
	if m.registrar, e = module.Resolve[*authz.Registrar](s); e != nil {
		return e
	}
	actions := []authz.Action{}
	for _, a := range []struct{ r, v string }{{"planning_settings", "manage"}, {"goal", "read"}, {"goal", "manage"}, {"goal_member", "manage"}, {"plan", "submit"}, {"plan", "confirm"}, {"report", "read"}, {"report", "submit"}, {"report", "confirm"}} {
		actions = append(actions, authz.Action{Resource: a.r, Verb: a.v, Scope: authz.ActionScopeTenant, DataScoped: true, Summary: a.v + " planning " + a.r})
	}
	if e = m.registrar.Registry().Contribute(actions, nil); e != nil {
		return e
	}
	if e = m.registerInbox(s); e != nil {
		return e
	}
	cat, e := module.Resolve[events.Catalog](s)
	if e != nil {
		return e
	}
	if e = cat.AddTypes(Name, events.TypeSpec{DisplayNameKey: planningi18n.Namespace + ".activity_event_loop_run_completed_name", DescriptionKey: planningi18n.Namespace + ".activity_event_loop_run_completed_description", Name: "loop_run.completed", SchemaVersion: 1, SubjectKind: "loop_run", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: planningi18n.Namespace + ".activity_event_loop_run_escalated_name", DescriptionKey: planningi18n.Namespace + ".activity_event_loop_run_escalated_description", Name: "loop_run.escalated", SchemaVersion: 1, SubjectKind: "loop_run", Realtime: events.RealtimeSignal}); e != nil {
		return e
	}
	if e = cat.AddTypes(Name, events.TypeSpec{DisplayNameKey: planningi18n.Namespace + ".activity_event_planning_report_checked_name", DescriptionKey: planningi18n.Namespace + ".activity_event_planning_report_checked_description", Name: "planning.report_checked", SchemaVersion: 1, SubjectKind: "planning_report", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: planningi18n.Namespace + ".activity_event_planning_report_overdue_name", DescriptionKey: planningi18n.Namespace + ".activity_event_planning_report_overdue_description", Name: "planning.report_overdue", SchemaVersion: 1, SubjectKind: "planning_report", Realtime: events.RealtimeSignal}); e != nil {
		return e
	}
	if e = cat.AddTypes(Name, events.TypeSpec{DisplayNameKey: planningi18n.Namespace + ".activity_event_planning_settings_updated_name", DescriptionKey: planningi18n.Namespace + ".activity_event_planning_settings_updated_description", Name: "planning.settings_updated", SchemaVersion: 1, SubjectKind: "planning_settings", Realtime: events.RealtimeSignal}); e != nil {
		return e
	}
	if e = cat.AddTypes(Name, events.TypeSpec{DisplayNameKey: planningi18n.Namespace + ".activity_event_loop_contract_state_changed_name", DescriptionKey: planningi18n.Namespace + ".activity_event_loop_contract_state_changed_description", Name: "loop_contract.state_changed", SchemaVersion: 1, SubjectKind: "loop_contract", Realtime: events.RealtimeSignal}); e != nil {
		return e
	}
	return cat.AddTypes(Name, events.TypeSpec{DisplayNameKey: planningi18n.Namespace + ".activity_event_planning_heavy_changed_name", DescriptionKey: planningi18n.Namespace + ".activity_event_planning_heavy_changed_description", Name: "planning.heavy_changed", SchemaVersion: 1, SubjectKind: "heavy_run", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: planningi18n.Namespace + ".activity_event_planning_changed_name", DescriptionKey: planningi18n.Namespace + ".activity_event_planning_changed_description", Name: "planning.changed", SchemaVersion: 1, SubjectKind: "planning_record", Realtime: events.RealtimeSignal})
}

var (
	_ module.Initializer             = (*Module)(nil)
	_ module.Enabler                 = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)
