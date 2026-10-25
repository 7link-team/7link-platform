package planning

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
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/notification"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	pi "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"path/filepath"
	"testing"
	"time"
)

func TestConfidenceDropRequiresStructuredOwnedDatedImprovement(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	r := reportDraft(t, h)
	prior := model.Report{TenantID: 1, ID: 10, ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-12", State: "CONFIRMED", Content: `{"declared_confidence":"GREEN"}`, ContentHash: "prior", Version: 1, CreatedBy: 2, Facts: "{}"}
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Create(&prior).Error)
	body := PlanningReportInput{Completed: []PlanningCompletion{{WorkItemID: 100}}, Uncompleted: "Pending work", Retrospective: "Learn from actual facts", NextPlan: []PlanningPlanItem{linkedItem(g)}, DeclaredConfidence: "RED"}
	check := func() PlanningCheck {
		t.Helper()
		var result PlanningCheck
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			row, _, e := h.m.reportOn(claims(t, 2), tx, 1, r.ID, "report_read", false)
			if e != nil {
				return e
			}
			encoded, _, e := hashJSON(body)
			if e != nil {
				return e
			}
			row.Content = encoded
			result, e = h.m.checkReport(claims(t, 2), tx, 1, row)
			return e
		}))
		return result
	}
	require.Contains(t, check().Issues, PlanningIssue{Code: "B7_CONFIDENCE_DROP_NO_PLAN", Blocking: true})
	body.ImprovementPlan = []PlanningImprovement{{Action: "Review evidence earlier", OwnerPrincipalID: 2, DueDate: "2026-10-25"}}
	require.NotContains(t, check().Issues, PlanningIssue{Code: "B7_CONFIDENCE_DROP_NO_PLAN", Blocking: true})
	for _, item := range []PlanningImprovement{
		{Action: "Review", DueDate: "2026-10-25"},
		{Action: "Review", OwnerPrincipalID: 2},
		{Action: "Review", OwnerPrincipalID: 2, DueDate: "2026-02-30"},
		{OwnerPrincipalID: 2, DueDate: "2026-10-25"},
	} {
		body.ImprovementPlan = []PlanningImprovement{item}
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.editReportOn(claims(t, 2), tx, 1, r.ID, r.Version, body)
			return e
		}), ErrInvalid)
		require.Contains(t, check().Issues, PlanningIssue{Code: "B7_CONFIDENCE_DROP_NO_PLAN", Blocking: true})
	}
	body.DeclaredConfidence = "GREEN"
	body.ImprovementPlan = nil
	require.NotContains(t, check().Issues, PlanningIssue{Code: "B7_CONFIDENCE_DROP_NO_PLAN", Blocking: true})
}

type testInboxSources struct{}

type failingPlanningIDs struct {
	value int64
	err   error
}

func (s failingPlanningIDs) Next() (int64, error) { return s.value, s.err }

func TestCreationRejectsWrongPayloadAndRollsBackInfrastructureFailure(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	base := PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}}
	for _, tc := range []struct {
		name, kind string
		mutate     func(*PlanningWriteInput)
		expected   error
	}{
		{"invalidperiod", "plan", func(in *PlanningWriteInput) { in.PeriodStart = "bad" }, ErrInvalid},
		{"missingprincipal", "plan", func(in *PlanningWriteInput) { in.PrincipalID = 0 }, ErrInvalid},
		{"negativeproject", "plan", func(in *PlanningWriteInput) { in.ProjectID = -1 }, ErrInvalid},
		{"otherauthor", "plan", func(in *PlanningWriteInput) { in.PrincipalID = 3 }, ErrForbidden},
		{"missingplan", "plan", func(in *PlanningWriteInput) { in.Plan = nil }, ErrInvalid},
		{"mixedpayload", "plan", func(in *PlanningWriteInput) { in.Report = &PlanningReportInput{} }, ErrInvalid},
		{"invaliditems", "plan", func(in *PlanningWriteInput) { in.Plan = &PlanningPlanInput{} }, ErrInvalid},
		{"suppliedreport", "report", func(in *PlanningWriteInput) { in.Plan = nil; in.Report = &PlanningReportInput{} }, ErrInvalid},
		{"unknownkind", "unknown", func(in *PlanningWriteInput) {}, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mutate(&in)
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.createOn(claims(t, 2), tx, 1, tc.kind, in); return e }), tc.expected)
		})
	}
	failure := errors.New("identifier allocator unavailable")
	ids := h.m.ids
	for _, tc := range []struct {
		ids      guid.Source
		expected error
	}{{nil, ErrUnavailable}, {failingPlanningIDs{}, ErrUnavailable}, {failingPlanningIDs{err: failure}, failure}} {
		h.m.ids = tc.ids
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.createOn(claims(t, 2), tx, 1, "plan", base); return e }), tc.expected)
	}
	h.m.ids = ids
	appender := h.m.audit
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.createOn(claims(t, 2), tx, 1, "plan", base); return e }), failure)
	h.m.audit = appender
	var count int64
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Count(&count).Error)
	require.Zero(t, count, "failed commands cannot persist a draft")
}

func TestReportDecisionsRecheckEvidenceAndRollBackAudit(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	r := reportDraft(t, h)
	body := PlanningReportInput{Completed: []PlanningCompletion{{WorkItemID: 100}}, Uncompleted: "Remaining work", Retrospective: "Independent evidence", NextPlan: []PlanningPlanItem{linkedItem(g)}, DeclaredConfidence: "YELLOW"}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = h.m.editReportOn(claims(t, 2), tx, 1, r.ID, r.Version, body)
		return e
	}))
	decide := func(actor guid.ID, in PlanningDecisionInput) error {
		return h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.decideReportOn(claims(t, actor), tx, 1, r.ID, in); return e })
	}
	in := PlanningDecisionInput{Version: r.Version, ExpectedContentHash: r.ContentHash, Action: "SUBMIT"}
	require.ErrorIs(t, decide(3, in), ErrForbidden)
	stale := in
	stale.ExpectedContentHash = "wrong"
	require.ErrorIs(t, decide(2, stale), ErrConflict)
	wrong := in
	wrong.Action = "CONFIRM"
	require.ErrorIs(t, decide(3, wrong), ErrConflict)
	wrong.Action = "UNKNOWN"
	require.ErrorIs(t, decide(3, wrong), ErrInvalid)
	failure := errors.New("audit unavailable")
	appender := h.m.audit
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, decide(2, in), failure)
	h.m.audit = appender
	require.NoError(t, decide(2, in))
	in.Version++
	in.Action = "CONFIRM"
	// Acceptance revoked after submission must prevent confirmation.
	h.facts.Work[0].State = "IN_PROGRESS"
	require.ErrorIs(t, decide(3, in), ErrConflict)
	h.facts.Work[0].State = "ACCEPTED"
	in.Action = "RETURN"
	in.Reason = "bad"
	require.ErrorIs(t, decide(3, in), ErrInvalid)
	in.Reason = "Recheck the current evidence"
	require.NoError(t, decide(3, in))
	in.Version++
	in.Action = "CONFIRM"
	require.ErrorIs(t, decide(3, in), ErrConflict)
}

func (testInboxSources) Register(governance.InboxSource) error { return nil }

type members struct{}

func (members) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) { return true, nil }
func (members) IsMemberActiveOn(_ context.Context, _ *gorm.DB, t, a guid.ID) (bool, error) {
	return t == 1 && a != 9, nil
}
func claims(t *testing.T, id guid.ID) context.Context {
	return authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: id, SubjectType: authn.SubjectTypePrincipal})
}

type harness struct {
	m     *Module
	db    *gorm.DB
	s     *module.Services
	facts *delivery.PlanningSnapshot
}

func setup(t *testing.T) harness {
	t.Helper()
	router, e := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "planning.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, e)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("p_"), appenv.WithDatabase(router))
	m := NewModule(rt, Config{Config: module.Config{Enabled: true}})
	db := router.Write()
	s := module.NewServices()
	require.NoError(t, module.Provide[fleet.WorkbenchMachines](s, func(context.Context, *gorm.DB, guid.ID) ([]fleet.MachineLoad, error) {
		return []fleet.MachineLoad{}, nil
	}))
	require.NoError(t, module.Provide[agents.ReminderLoops](s, agents.ReminderLoops{}))
	require.NoError(t, module.Provide[collab.ThreadAnchor](s, collab.ThreadAnchor{CheckOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) error { return nil }}))
	ids, e := guid.New(1)
	require.NoError(t, e)
	require.NoError(t, module.Provide[guid.Source](s, ids))
	require.NoError(t, module.Provide[identity.PrincipalStateStore](s, identity.PrincipalStateStore{LockActiveRow: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id > 0 && id != 8, nil
	}, ActiveHumanByID: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id != 7, nil
	}}))
	require.NoError(t, module.Provide[core.ActiveMemberChecker](s, members{}))
	require.NoError(t, module.Provide[core.TenantConstraintOn](s, func(_ context.Context, _ *gorm.DB, _ guid.ID, _ string, a guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: a != 6}, nil
	}))
	require.NoError(t, module.Provide[core.EntityConstraintOn](s, func(_ context.Context, _ *gorm.DB, _ guid.ID, p guid.ID, _ string, a guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: p == 3 && a != 6}, nil
	}))
	require.NoError(t, module.Provide[fleet.AgentDirectory](s, fleet.AgentDirectory{ByPrincipalOn: func(_ context.Context, _ *gorm.DB, tenant, a guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 70, TenantID: tenant, PrincipalID: a, OwnerPrincipalID: 2, State: "ACTIVE"}, a == 7, nil
	}}))
	require.NoError(t, module.Provide[delivery.ReportSuperior](s, func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 3, nil }))
	require.NoError(t, module.Provide[delivery.ProjectScope](s, delivery.ProjectScope{ArchivedOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil }, AuthorizeOn: func(_ context.Context, _ *gorm.DB, tenant, p guid.ID, _ string) error {
		if tenant != 1 || p != 3 {
			return ErrForbidden
		}
		return nil
	}}))
	accepted := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	facts := &delivery.PlanningSnapshot{Work: []delivery.PlanningWorkFact{{ID: 100, Title: "Accepted work", State: "ACCEPTED", AccountablePrincipalID: 2, AcceptedAt: &accepted}, {ID: 101, Title: "Unaccepted result", State: "IN_PROGRESS", AccountablePrincipalID: 2}}, ResultCount: 2, HandoffCount: 1, MissingSources: []string{"EXECUTION_COST", "CLIENT_ACCEPTANCE"}}
	require.NoError(t, module.Provide[delivery.PlanningFacts](s, delivery.PlanningFacts{ReadOn: func(_ context.Context, _ *gorm.DB, _, _, subject guid.ID, _, _ time.Time) (delivery.PlanningSnapshot, error) {
		v := *facts
		v.Work = []delivery.PlanningWorkFact{}
		for _, f := range facts.Work {
			if subject == 0 || f.AccountablePrincipalID == subject {
				v.Work = append(v.Work, f)
			}
		}
		return v, nil
	}}))
	require.NoError(t, module.Provide[delivery.ScheduledPlanningFacts](s, delivery.ScheduledPlanningFacts{}))
	require.NoError(t, module.Provide[execution.PeriodReader](s, execution.PeriodReader{}))
	require.NoError(t, module.Provide[execution.LoopExecutionFacts](s, execution.LoopExecutionFacts{}))
	require.NoError(t, module.Provide[delivery.LoopAssignments](s, delivery.LoopAssignments{}))
	require.NoError(t, module.Provide[budget.SettlementFacts](s, budget.SettlementFacts{}))
	require.NoError(t, module.Provide[delivery.ProjectBudgets](s, delivery.ProjectBudgets{}))
	require.NoError(t, db.Exec("CREATE TABLE audit_probe(event_type TEXT NOT NULL)").Error)
	require.NoError(t, module.Provide[audit.Appender](s, func(ctx context.Context, tx *gorm.DB, ev audit.DomainEvent) error {
		require.NotEmpty(t, ev.Detail["authority_source"])
		return tx.WithContext(ctx).Exec("INSERT INTO audit_probe VALUES (?)", ev.EventType).Error
	}))
	locks := dlock.NewModule(rt)
	require.NoError(t, locks.ProvideServices(s))
	ev := events.NewModule(rt)
	require.NoError(t, ev.ProvideServices(s))
	idem := idempotency.NewModule(rt)
	require.NoError(t, idem.ProvideServices(s))
	registry, e := authz.NewRegistry()
	require.NoError(t, e)
	// Delivery owns this action in the composed app; the isolated Planning
	// harness must declare it for the workbench route as well.
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Read current work items"}}, nil))
	require.NoError(t, module.Provide[*authz.Registrar](s, authz.NewDeclarationOnlyRegistrar(registry)))
	require.NoError(t, module.Provide[governance.InboxSources](s, testInboxSources{}))
	require.NoError(t, module.Provide[clientwork.CollaborationInbox](s, func(context.Context, *gorm.DB, guid.ID, guid.ID, string) ([]clientwork.CollaborationInboxItem, error) {
		return nil, nil
	}))
	require.NoError(t, db.Exec("CREATE TABLE planning_notice_probe(recipient BIGINT, report_id TEXT)").Error)
	require.NoError(t, module.Provide[notification.Notifier](s, planningTestNotifier{}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.InjectServices(s))
	require.NoError(t, ev.InjectServices(s))
	require.NoError(t, idem.InjectServices(s))
	require.NoError(t, pi.NewModule(rt, pi.Base).Initialize(t.Context()))
	require.NoError(t, ev.Initialize(t.Context()))
	require.NoError(t, idem.Initialize(t.Context()))
	raw, e := db.DB()
	require.NoError(t, e)
	for _, source := range []goosex.Source{m.MigrationSource(), locks.MigrationSource(), ev.MigrationSource(), idem.MigrationSource()} {
		source.Prefix = "p_"
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	}
	m.now = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
	return harness{m, db, s, facts}
}
func create(t *testing.T, h harness, kind string, in PlanningWriteInput) PlanningRecord {
	t.Helper()
	var r PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { var e error; r, e = h.m.createOn(claims(t, 2), tx, 1, kind, in); return e }))
	return r
}
func goalTree(t *testing.T, h harness) PlanningRecord {
	t.Helper()
	base := PlanningWriteInput{PrincipalID: 2, PeriodKind: "QUARTER", PeriodStart: "2026-10-01", Goal: &PlanningGoalInput{Level: "TEAM", Title: "Team objective", KeyResults: []PlanningKeyResult{{Key: "delivery", Title: "Accepted output", Source: "MANUAL", Target: 10}}}}
	team := activateGoal(t, h, create(t, h, "goal", base))
	base.PeriodKind = "MONTH"
	base.ProjectID = 3
	base.Goal.Level = "PROJECT"
	base.Goal.ParentGoalID = &team.ID
	base.Goal.ParentKR = "delivery"
	project := activateGoal(t, h, create(t, h, "goal", base))
	base.Goal.Level = "MEMBER"
	base.Goal.ParentGoalID = &project.ID
	return activateGoal(t, h, create(t, h, "goal", base))
}
func linkedItem(goal PlanningRecord) PlanningPlanItem {
	return PlanningPlanItem{Title: "Learn from accepted work", GoalID: goal.ID, KR: "delivery", Category: "LEARNING"}
}
func reportDraft(t *testing.T, h harness) PlanningRecord {
	t.Helper()
	return create(t, h, "report", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-19"})
}
func TestReportsRequireCurrentPeriodEvidenceAndIndependentHumanConfirmation(t *testing.T) {
	h := setup(t)
	goal := goalTree(t, h)
	draft := reportDraft(t, h)
	require.Equal(t, "DRAFT", draft.State)
	var content PlanningReportInput
	require.NoError(t, json.Unmarshal(draft.Content, &content))
	require.Len(t, content.Completed, 1)
	require.Equal(t, guid.ID(100), content.Completed[0].WorkItemID)
	require.Contains(t, draft.Facts.MissingSources, "EXECUTION_COST")
	require.EqualValues(t, 1, draft.Facts.HandoffCount)
	content.Uncompleted = "One work item remains in progress"
	content.Retrospective = "Use independent verification"
	content.NextPlan = []PlanningPlanItem{linkedItem(goal)}
	content.DeclaredConfidence = "YELLOW"
	var edited PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		edited, e = h.m.editReportOn(claims(t, 2), tx, 1, draft.ID, 1, content)
		return e
	}))
	require.Equal(t, 2, edited.Version)
	submit := PlanningDecisionInput{Version: 2, Action: "SUBMIT", ExpectedContentHash: edited.ContentHash}
	var submitted PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		submitted, e = h.m.decideReportOn(claims(t, 2), tx, 1, draft.ID, submit)
		return e
	}))
	require.Equal(t, "SUBMITTED", submitted.State)
	confirm := PlanningDecisionInput{Version: 3, Action: "CONFIRM", ExpectedContentHash: submitted.ContentHash}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.decideReportOn(claims(t, 2), tx, 1, draft.ID, confirm); return e }), ErrForbidden)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.decideReportOn(claims(t, 7), tx, 1, draft.ID, confirm); return e }), ErrForbidden)
	var confirmed PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		confirmed, e = h.m.decideReportOn(claims(t, 3), tx, 1, draft.ID, confirm)
		return e
	}))
	require.Equal(t, "CONFIRMED", confirmed.State)
	require.Equal(t, guid.ID(3), *confirmed.ConfirmedBy)
	require.NotNil(t, confirmed.ConfirmedAt)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.editReportOn(claims(t, 2), tx, 1, draft.ID, confirmed.Version, content)
		return e
	}), ErrConflict)
}
func TestReconciliationNeverAcceptsUnbackedDuplicateOrLateCompletion(t *testing.T) {
	for _, mode := range []string{"missing_sections", "unknown_work", "duplicate", "late_acceptance", "revoked_acceptance", "unlinked_next"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			goal := goalTree(t, h)
			r := reportDraft(t, h)
			in := PlanningReportInput{Completed: []PlanningCompletion{{WorkItemID: 100}}, Uncompleted: "Pending work", Retrospective: "Review actual evidence", NextPlan: []PlanningPlanItem{linkedItem(goal)}, DeclaredConfidence: "YELLOW"}
			switch mode {
			case "missing_sections":
				in.Retrospective = ""
			case "unknown_work":
				in.Completed[0].WorkItemID = 999
			case "duplicate":
				in.Completed = append(in.Completed, in.Completed[0])
			case "late_acceptance":
				late := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
				h.facts.Work[0].AcceptedAt = &late
			case "revoked_acceptance":
				h.facts.Work[0].State = "REWORK"
			case "unlinked_next":
				in.NextPlan[0].GoalID = 999
			}
			var edited PlanningRecord
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				var e error
				edited, e = h.m.editReportOn(claims(t, 2), tx, 1, r.ID, 1, in)
				return e
			}))
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.decideReportOn(claims(t, 2), tx, 1, r.ID, PlanningDecisionInput{Version: 2, Action: "SUBMIT", ExpectedContentHash: edited.ContentHash})
				return e
			}), ErrConflict)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				row, _, e := h.m.reportOn(claims(t, 2), tx, 1, r.ID, "report_read", false)
				require.NoError(t, e)
				check, e := h.m.checkReport(claims(t, 2), tx, 1, row)
				require.Equal(t, "BLOCKED", check.Result)
				return e
			}))
		})
	}
}
func TestPlanItemsRequireAlignedMemberGoalAndRealSubjectWork(t *testing.T) {
	h := setup(t)
	goal := goalTree(t, h)
	input := PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(goal)}}}
	p := create(t, h, "plan", input)
	require.Equal(t, "DRAFT", p.State)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.decidePlanOn(claims(t, 2), tx, 1, p.ID, PlanningDecisionInput{Version: 1, Action: "SUBMIT", ExpectedContentHash: p.ContentHash})
		return e
	}))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.decidePlanOn(claims(t, 2), tx, 1, p.ID, PlanningDecisionInput{Version: 2, Action: "CONFIRM", ExpectedContentHash: p.ContentHash})
		return e
	}), ErrForbidden)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.decidePlanOn(claims(t, 3), tx, 1, p.ID, PlanningDecisionInput{Version: 2, Action: "CONFIRM", ExpectedContentHash: p.ContentHash})
		return e
	}))
	for _, mode := range []string{"foreign_goal", "foreign_kr", "missing_goal", "invalid_category", "missing_work", "foreign_subject"} {
		t.Run(mode, func(t *testing.T) {
			item := linkedItem(goal)
			switch mode {
			case "foreign_goal":
				item.GoalID = 999
			case "foreign_kr":
				item.KR = "unknown"
			case "missing_goal":
				item.GoalID = 0
			case "invalid_category":
				item.Category = "OTHER_UNKNOWN"
			case "missing_work":
				id := guid.ID(999)
				item.WorkItemID = &id
				item.Category = ""
			case "foreign_subject":
			}
			subject := guid.ID(2)
			if mode == "foreign_subject" {
				subject = 3
			}
			require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.validateItems(claims(t, 2), tx, 1, 3, subject, []PlanningPlanItem{item})
			}))
		})
	}
	id := guid.ID(100)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.validateItems(claims(t, 2), tx, 1, 3, 2, []PlanningPlanItem{{Title: "Actual work", GoalID: goal.ID, KR: "delivery", WorkItemID: &id}})
	}))
}
func TestPlanningAuthorityAndTransactionsFailClosed(t *testing.T) {
	h := setup(t)
	for _, actor := range []guid.ID{0, 6, 8, 9} {
		require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.authorize(claims(t, actor), tx, 1, 3, actor, "report_write", false)
			return e
		}))
	}
	require.ErrorIs(t, func() error { _, e := h.m.authorize(claims(t, 2), h.db, 1, 3, 2, "report_read", false); return e }(), ErrInvalid)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.authorize(claims(t, 2), tx, 2, 3, 2, "report_read", false)
		return e
	}), ErrForbidden)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		kind, e := h.m.authorize(claims(t, 7), tx, 1, 3, 7, "report_write", false)
		require.Equal(t, "AGENT", kind)
		return e
	}))
	original := h.m.audit
	failure := errors.New("audit offline")
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.createOn(claims(t, 2), tx, 1, "goal", PlanningWriteInput{PrincipalID: 2, PeriodKind: "QUARTER", PeriodStart: "2026-10-01", Goal: &PlanningGoalInput{Level: "TEAM", Title: "Rollback", KeyResults: []PlanningKeyResult{{Key: "x", Title: "Metric", Source: "MANUAL", Target: 5}}}})
		return e
	}), failure)
	q := query.BindGoals(h.db, h.m.names)
	n, e := q.WithContext(t.Context()).Count()
	require.NoError(t, e)
	require.Zero(t, n)
	h.m.audit = original
}
func TestPeriodBoundariesAreCalendarExact(t *testing.T) {
	for _, c := range []struct{ k, s, end string }{{"DAY", "2028-02-29", "2028-03-01"}, {"WEEK", "2026-10-19", "2026-10-26"}, {"MONTH", "2028-02-01", "2028-03-01"}, {"QUARTER", "2026-10-01", "2027-01-01"}, {"YEAR", "2028-01-01", "2029-01-01"}} {
		_, end, e := period(c.k, c.s)
		require.NoError(t, e)
		require.Equal(t, c.end, end.Format("2006-01-02"))
	}
	for _, c := range []struct{ k, s string }{{"BAD", "2026-01-01"}, {"WEEK", "2026-10-20"}, {"MONTH", "2026-10-02"}, {"QUARTER", "2026-11-01"}, {"YEAR", "2026-02-01"}, {"DAY", "2026-02-30"}} {
		_, _, e := period(c.k, c.s)
		require.ErrorIs(t, e, ErrInvalid)
	}
}

func activateGoal(t *testing.T, h harness, r PlanningRecord) PlanningRecord {
	t.Helper()
	var next PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		next, err = h.m.changeGoalOn(claims(t, 2), tx, 1, r.ID, PlanningGoalChange{Version: r.Version, ExpectedContentHash: r.ContentHash, Action: "ACTIVATE"})
		return err
	}))
	return next
}

func TestSnapshotsIncludeOnlyRealExecutionFactsAndKeepMissingSourcesExplicit(t *testing.T) {
	h := setup(t)
	start, end, err := period("WEEK", "2026-10-19")
	require.NoError(t, err)
	read := func() (delivery.PlanningSnapshot, error) {
		var facts delivery.PlanningSnapshot
		err := h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			facts, e = h.m.snapshotOn(claims(t, 2), tx, 1, 3, 2, start, end)
			return e
		})
		return facts, err
	}
	facts, err := read()
	require.NoError(t, err)
	require.Nil(t, facts.ExecutionCreated)
	require.Contains(t, facts.MissingSources, "EXECUTIONS")
	for _, failure := range []error{execution.ErrForbidden, execution.ErrUnavailable} {
		h.m.executions.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (execution.PeriodFacts, error) {
			return execution.PeriodFacts{}, failure
		}
		facts, err = read()
		require.NoError(t, err)
		require.Contains(t, facts.MissingSources, "EXECUTIONS")
		require.Nil(t, facts.ExecutionCreated)
	}
	failure := errors.New("execution storage unavailable")
	h.m.executions.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (execution.PeriodFacts, error) {
		return execution.PeriodFacts{}, failure
	}
	_, err = read()
	require.ErrorIs(t, err, failure)
	h.m.executions.ReadOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, ids []guid.ID, from, to time.Time) (execution.PeriodFacts, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		require.Equal(t, []guid.ID{100, 101}, ids)
		require.Equal(t, start, from)
		require.Equal(t, end, to)
		return execution.PeriodFacts{Created: 2, States: map[string]int64{"RUNNING": 1, "FAILED": 1}}, nil
	}
	facts, err = read()
	require.NoError(t, err)
	require.EqualValues(t, 2, *facts.ExecutionCreated)
	require.Equal(t, map[string]int64{"RUNNING": 1, "FAILED": 1}, facts.ExecutionStates)
	require.NotContains(t, facts.MissingSources, "EXECUTIONS")
	require.Contains(t, facts.MissingSources, "EXECUTION_COST")
	h.m.facts.ReadOn = nil
	_, err = read()
	require.ErrorIs(t, err, ErrUnavailable)
}

type failingMemberChecker struct{ err error }

func (f failingMemberChecker) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return false, f.err
}
func (f failingMemberChecker) IsMemberActiveOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
	return false, f.err
}

func TestPlanningAuthorityDoesNotSwallowProviderFailuresOrExplicitDenials(t *testing.T) {
	h := setup(t)
	failure := errors.New("authority source unavailable")
	for _, mode := range []string{"principal", "membership", "human", "agent", "tenant_policy", "entity_policy", "project"} {
		t.Run(mode, func(t *testing.T) {
			m := *h.m
			actor, project := guid.ID(2), guid.ID(3)
			switch mode {
			case "principal":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			case "membership":
				m.members = failingMemberChecker{failure}
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			case "agent":
				actor = 7
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, failure
				}
			case "tenant_policy":
				project = 0
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "entity_policy":
				m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "project":
				m.project.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return failure }
			}
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
				_, e := m.authorize(claims(t, actor), tx, 1, project, actor, "goal_read", false)
				return e
			}), failure)
		})
	}
	for _, constraint := range []authz.DataConstraint{{OwnerIDs: []string{"2"}}, {ResourceIDs: []string{"3"}}, {AllowAll: true, DeniedIDs: []string{"3"}}, {OwnerIDs: []string{"99"}}} {
		m := *h.m
		m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return constraint, nil
		}
		err := h.db.Transaction(func(tx *gorm.DB) error { _, e := m.authorize(claims(t, 2), tx, 1, 3, 2, "goal_read", false); return e })
		if len(constraint.DeniedIDs) > 0 || (len(constraint.OwnerIDs) > 0 && constraint.OwnerIDs[0] == "99") {
			require.ErrorIs(t, err, ErrForbidden)
		} else {
			require.NoError(t, err)
		}
	}
}

func TestPlanningCreateSourceAndPersistenceFailuresLeaveNoNewReport(t *testing.T) {
	for _, mode := range []string{"missing_facts", "facts", "settings", "ids_missing", "ids_error", "ids_zero", "audit_missing", "events_missing", "insert", "audit"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			in := PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20"}
			switch mode {
			case "missing_facts":
				h.m.facts.ReadOn = nil
			case "facts":
				h.m.facts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
					return delivery.PlanningSnapshot{}, ErrUnavailable
				}
			case "settings":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("create_report_settings", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Settings.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "ids_missing":
				h.m.ids = nil
			case "ids_error":
				h.m.ids = failingPlanningIDs{err: ErrUnavailable}
			case "ids_zero":
				h.m.ids = failingPlanningIDs{}
			case "audit_missing":
				h.m.audit = nil
			case "events_missing":
				h.m.events = nil
			case "insert":
				require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("create_report_insert", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Reports.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "audit":
				h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return ErrUnavailable }
			}
			err := h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.createOn(claims(t, 2), tx, 1, "report", in); return e })
			require.ErrorIs(t, err, ErrUnavailable)
			require.NoError(t, h.db.Callback().Query().Remove("create_report_settings"))
			require.NoError(t, h.db.Callback().Create().Remove("create_report_insert"))
			var n int64
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Count(&n).Error)
			require.Zero(t, n)
		})
	}
}

func TestReportEditRejectsCorruptAndFailedSourcesWithoutPublishing(t *testing.T) {
	for _, mode := range []string{"content", "plans", "plan_content", "write", "audit", "events"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			g := goalTree(t, h)
			r := reportDraft(t, h)
			var body PlanningReportInput
			require.NoError(t, json.Unmarshal(r.Content, &body))
			body.Uncompleted = "Pending work"
			body.Retrospective = "Actual evidence"
			body.NextPlan = []PlanningPlanItem{linkedItem(g)}
			body.DeclaredConfidence = "YELLOW"
			switch mode {
			case "content":
				require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).Update("content", "[").Error)
			case "plans":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("report_edit_plans", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Plans.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "plan_content":
				p := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-19", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
				require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", p.ID).Update("content", "[").Error)
			case "write":
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("report_edit_write", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Reports.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "audit":
				h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return ErrUnavailable }
			case "events":
				h.m.events = nil
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.editReportOn(claims(t, 2), tx, 1, r.ID, r.Version, body)
				return e
			})
			require.Error(t, err)
			require.NoError(t, h.db.Callback().Query().Remove("report_edit_plans"))
			require.NoError(t, h.db.Callback().Update().Remove("report_edit_write"))
			var row model.Report
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&row).Error)
			require.Equal(t, r.Version, row.Version)
		})
	}
}

func TestReportCheckDoesNotTurnInvalidPeriodOrMissingFactsIntoPass(t *testing.T) {
	for _, mode := range []string{"content", "period", "missing", "source"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			r := reportDraft(t, h)
			var row model.Report
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&row).Error)
			switch mode {
			case "content":
				row.Content = "["
			case "period":
				row.Content = "{}"
				row.PeriodStart = "bad"
			case "missing":
				h.m.facts.ReadOn = nil
			case "source":
				h.m.facts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
					return delivery.PlanningSnapshot{}, ErrUnavailable
				}
			}
			require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.checkReport(claims(t, 2), tx, 1, &row); return e }))
		})
	}
}
