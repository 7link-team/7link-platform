package planning

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type loopRuntimeServices struct {
	executions  execution.LoopExecutionFacts
	assignments delivery.LoopAssignments
	settlements budget.SettlementFacts
	projects    delivery.ProjectBudgets
}

func (m *Module) injectLoopRuntime(s *module.Services) error {
	var e error
	if m.loopRuntime.executions, e = module.Resolve[execution.LoopExecutionFacts](s); e != nil {
		return e
	}
	if m.loopRuntime.assignments, e = module.Resolve[delivery.LoopAssignments](s); e != nil {
		return e
	}
	if m.loopRuntime.settlements, e = module.Resolve[budget.SettlementFacts](s); e != nil {
		return e
	}
	m.loopRuntime.projects, e = module.Resolve[delivery.ProjectBudgets](s)
	return e
}
func (m *Module) loopRuntimeAvailable() bool {
	return m.loopRuntime.executions.ReadOn != nil && m.loopRuntime.assignments.AgentsOn != nil && m.loopRuntime.settlements.ExecutionsOn != nil && m.loopRuntime.projects.LockOn != nil
}
func (m *Module) loopCostOn(ctx context.Context, tx *gorm.DB, contract *model.LoopContract, start, end time.Time) (int64, error) {
	if contract.ProjectID == nil {
		return m.teamLoopCostOn(ctx, tx, contract, start, end)
	}
	if !m.loopRuntimeAvailable() {
		return 0, ErrUnavailable
	}
	facts, e := m.loopRuntime.executions.ReadOn(ctx, tx, contract.TenantID, *contract.ProjectID, nil, start, end)
	if e != nil {
		return 0, e
	}
	ids := make([]guid.ID, 0, len(facts))
	for _, f := range facts {
		ids = append(ids, f.AssignmentID)
	}
	holders, e := m.loopRuntime.assignments.AgentsOn(ctx, tx, contract.TenantID, *contract.ProjectID, ids)
	if e != nil {
		return 0, e
	}
	executionIDs := []guid.ID{}
	for _, fact := range facts {
		agent, found := holders[fact.AssignmentID]
		if !found {
			return 0, ErrUnavailable
		}
		if agent != contract.AgentID {
			continue
		}
		if fact.StartedAt == nil {
			return 0, ErrUnavailable
		}
		executionIDs = append(executionIDs, fact.ID)
	}
	cost, settled, e := m.loopRuntime.settlements.ExecutionsOn(ctx, tx, contract.TenantID, *contract.ProjectID, executionIDs)
	if e != nil {
		return 0, e
	}
	if !settled {
		return 0, ErrUnavailable
	}
	return cost, nil
}

// Lock principal -> project -> contract -> run, retaining current authority on replays.
func (m *Module) authorizeLoopResultOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (*model.LoopContract, *model.LoopRun, error) {
	q := query.BindLoopRuns(tx, m.names)
	run, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Take()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, nil, ErrNotFound
	}
	if e != nil {
		return nil, nil, e
	}
	c := query.BindLoopContracts(tx, m.names)
	ref, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(run.LoopContractID)).Take()
	if e != nil {
		return nil, nil, e
	}
	if authn.PrincipalIDFromContext(ctx) != ref.AgentID || authn.TenantIDFromContext(ctx) != tenant {
		return nil, nil, ErrForbidden
	}
	if e = m.lockTeamLoopParticipantsOn(ctx, tx, tenant, ref); e != nil {
		return nil, nil, e
	}
	if m.principals.LockActiveRow == nil {
		return nil, nil, ErrUnavailable
	}
	if _, active, e := m.principals.LockActiveRow(ctx, tx, ref.AgentID); e != nil {
		return nil, nil, e
	} else if !active {
		return nil, nil, ErrForbidden
	}
	if m.loopRuntimeAvailable() && ref.ProjectID != nil {
		if _, e = m.loopRuntime.projects.LockOn(ctx, tx, tenant, *ref.ProjectID); e != nil {
			return nil, nil, e
		}
	}
	contract, e := m.loopOn(ctx, tx, tenant, ref.ID)
	if e != nil {
		return nil, nil, e
	}
	if e = m.loopAuthority(ctx, tx, tenant, contract, false); e != nil {
		return nil, nil, e
	}
	if m.agents.ByPrincipalOn == nil {
		return nil, nil, ErrUnavailable
	}
	agent, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, contract.AgentID)
	if e != nil {
		return nil, nil, e
	}
	if !found || agent.State != "ACTIVE" || agent.PrincipalID != contract.AgentID {
		return nil, nil, ErrForbidden
	}
	run, e = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Take()
	return contract, run, e
}
func (m *Module) completeLoopOn(ctx context.Context, tx *gorm.DB, contract *model.LoopContract, run *model.LoopRun, in *LoopResultInput) (LoopRunView, error) {
	var empty LoopRunView
	if run.State != "OPEN" {
		return empty, ErrConflict
	}
	if !m.loopRuntimeAvailable() {
		return empty, ErrUnavailable
	}
	if utf8.RuneCountInString(in.Body.Note) > 2000 || !utf8.ValidString(in.Body.Note) {
		return empty, ErrInvalid
	}
	definition, e := loopView(contract)
	if e != nil {
		return empty, e
	}
	now := m.now()
	if !now.After(run.OpenedAt) {
		now = run.OpenedAt.Add(time.Millisecond)
	}
	cost, e := m.loopCostOn(ctx, tx, contract, run.OpenedAt, now)
	if e != nil {
		return empty, e
	}
	reason := ""
	if !now.Before(run.OpenedAt.Add(time.Duration(definition.Budget.MaxDurationMinutes) * time.Minute)) {
		reason = "TIMED_OUT"
	}
	checks := map[string]bool{}
	for _, check := range in.Body.SelfCheckResults {
		if checks[check.Key] || utf8.RuneCountInString(check.Note) > 500 || !utf8.ValidString(check.Note) {
			return empty, ErrInvalid
		}
		checks[check.Key] = true
	}
	for key := range checks {
		known := false
		for _, defined := range definition.SelfChecks {
			if defined.Key == key {
				known = true
			}
		}
		if !known {
			return empty, ErrInvalid
		}
	}
	for _, required := range definition.SelfChecks {
		found := false
		for _, check := range in.Body.SelfCheckResults {
			if check.Key == required.Key {
				found = true
				if required.Required && !check.Passed && reason == "" {
					reason = "REQUIRED_CHECK_FAILED"
				}
			}
		}
		if !found && required.Required {
			return empty, ErrInvalid
		}
	}
	if contract.Kind == "CHECK" {
		if in.Body.CheckResult != nil {
			if len(in.Body.CheckResult.Findings) > 50 {
				return empty, ErrInvalid
			}
			for _, finding := range in.Body.CheckResult.Findings {
				if !validText(finding.Title, 200) || (finding.Severity != "INFO" && finding.Severity != "WARN" && finding.Severity != "FAIL") || (finding.Ref != nil && (finding.Ref.ID <= 0 || !validText(finding.Ref.Kind, 64))) {
					return empty, ErrInvalid
				}
			}
		}
		if in.Body.ReportID != nil || in.Body.CheckResult == nil || (in.Body.CheckResult.Status != "OK" && in.Body.CheckResult.Status != "WARN" && in.Body.CheckResult.Status != "FAIL") {
			return empty, ErrInvalid
		}
		if in.Body.CheckResult.Status == "FAIL" && reason == "" {
			reason = "CHECK_FAILED"
		}
	} else {
		if in.Body.ReportID == nil || in.Body.CheckResult != nil || definition.ReportBinding == nil {
			return empty, ErrInvalid
		}
		report, _, err := m.reportOn(ctx, tx, contract.TenantID, *in.Body.ReportID, "report_read", false)
		if err != nil {
			return empty, err
		}
		var content PlanningReportInput
		if json.Unmarshal([]byte(report.Content), &content) != nil {
			return empty, ErrInvalid
		}
		start, end, err := reportPeriod(report)
		if err != nil {
			return empty, err
		}
		scope := content.Scope
		if scope == "" {
			scope = "MEMBER"
		}
		periodMatches := !run.ScheduledFor.Before(start) && run.ScheduledFor.Before(end)
		if contract.ScheduleKind == "PERIOD" {
			settings, err := m.readSettingsOn(ctx, tx, contract.TenantID)
			if err != nil {
				return empty, err
			}
			expected, due := scheduledReportPeriod(definition.ReportBinding.PeriodKind, settings, run.ScheduledFor)
			periodMatches = due && report.PeriodStart == expected
		}
		if report.PeriodKind != definition.ReportBinding.PeriodKind || scope != definition.ReportBinding.Level || !periodMatches || report.ProjectID != loopProject(contract) || (scope == "MEMBER" && report.PrincipalID != contract.AgentID) {
			return empty, ErrInvalid
		}
		// Recheck against current facts; no report submission or Human confirmation is implied.
		check, err := m.checkReport(ctx, tx, contract.TenantID, report)
		if err != nil {
			return empty, err
		}
		if (report.State != "READY" && report.State != "SUBMITTED" && report.State != "CONFIRMED") || check.Result == "BLOCKED" {
			if reason == "" {
				reason = "REPORT_BLOCKED"
			}
		}
	}
	if reason != "TIMED_OUT" && cost > definition.Budget.MaxCostMicrosPerRun && definition.Escalation.OnBudgetExceeded {
		reason = "BUDGET_EXCEEDED"
	}
	monthlyExceeded, e := m.loopMonthlyExceededOn(ctx, tx, contract, run, definition, cost, now)
	if e != nil {
		return empty, e
	}
	if monthlyExceeded && reason != "TIMED_OUT" {
		reason = "MONTHLY_CAP_EXCEEDED"
	}

	run.State = "SUCCEEDED"
	if reason != "" {
		run.State = "ESCALATED"
	}
	if reason == "TIMED_OUT" {
		run.State = "TIMED_OUT"
	}
	run.ReportID = in.Body.ReportID
	run.SelfCheckResults = loopJSON(in.Body.SelfCheckResults)
	if in.Body.CheckResult != nil {
		raw := loopJSON(in.Body.CheckResult)
		run.CheckResult = &raw
	}
	run.CostMicros = cost
	run.CompletedAt = &now
	note := strings.TrimSpace(in.Body.Note)
	run.Note = &note
	if reason != "" {
		run.EscalationReason = &reason
	}
	if e = m.persistLoopCompletionOn(ctx, tx, contract, run, definition, monthlyExceeded); e != nil {
		return empty, e
	}
	return completedLoopRunView(run), nil
}
func completedLoopRunView(run *model.LoopRun) LoopRunView {
	out := loopRunView(run)
	out.UnavailableCapabilities = []string{}
	out.CostMicros = &run.CostMicros
	return out
}
func (m *Module) persistLoopCompletionOn(ctx context.Context, tx *gorm.DB, contract *model.LoopContract, run *model.LoopRun, definition LoopContractView, monthly bool) error {
	if m.events == nil || m.audit == nil {
		return ErrUnavailable
	}
	q := query.BindLoopRuns(tx, m.names)
	res, e := q.WithContext(ctx).Where(q.TenantID.Eq(run.TenantID), q.ID.Eq(run.ID), q.State.Eq("OPEN")).Updates(map[string]any{"state": run.State, "report_id": run.ReportID, "check_result": run.CheckResult, "self_check_results": run.SelfCheckResults, "cost_micros": run.CostMicros, "completed_at": run.CompletedAt, "escalation_reason": run.EscalationReason, "note": run.Note})
	if e != nil {
		return e
	}
	if res.RowsAffected != 1 {
		return ErrConflict
	}
	event := "loop_run.completed"
	if run.State == "ESCALATED" || run.State == "TIMED_OUT" {
		contract.ConsecutiveFailures++
		event = "loop_run.escalated"
	} else {
		contract.ConsecutiveFailures = 0
	}
	autoPaused := false
	if monthly || contract.ConsecutiveFailures >= definition.Escalation.ConsecutiveFailuresToPause {
		if contract.State == "ACTIVE" {
			if contract.ReminderID == nil || m.loopReminders.ChangeOn == nil {
				return ErrUnavailable
			}
			if e = m.loopReminders.ChangeOn(ctx, tx, contract.TenantID, contract.AgentID, *contract.ReminderID, "PAUSED"); e != nil {
				return e
			}
			contract.State = "PAUSED"
			autoPaused = true
		}
	}
	c := query.BindLoopContracts(tx, m.names)
	res, e = c.WithContext(ctx).Where(c.TenantID.Eq(contract.TenantID), c.ID.Eq(contract.ID), c.Version.Eq(contract.Version)).Updates(map[string]any{"state": contract.State, "consecutive_failures": contract.ConsecutiveFailures, "version": contract.Version + 1, "updated_at": m.now()})
	if e != nil {
		return e
	}
	if res.RowsAffected != 1 {
		return ErrConflict
	}
	contract.Version++
	actor := events.Actor{Kind: "AGENT", ID: contract.AgentID, AuthoritySource: "SELF:loop_run_result"}
	if authn.PrincipalIDFromContext(ctx) == 0 {
		actor = events.Actor{Kind: "SYSTEM", AuthoritySource: "scheduled:planning.loop_run_timeout"}
	}
	reason := ""
	if run.EscalationReason != nil {
		reason = *run.EscalationReason
	}
	auditEvent := audit.NewDomainEvent(ctx, contract.TenantID, "planning."+strings.Replace(event, ".", "_", 1), "loop_run", run.ID)
	auditEvent.Detail["authority_source"] = actor.AuthoritySource
	auditEvent.Detail["reason"] = reason
	auditEvent.Detail["state"] = run.State
	if run.State != "TIMED_OUT" {
		auditEvent.Detail["cost_micros"] = run.CostMicros
	}
	if e = m.audit(ctx, tx, auditEvent); e != nil {
		return e
	}
	_, e = m.events.Publish(ctx, tx, events.Event{TenantID: contract.TenantID, ProjectID: loopProject(contract), Type: event, Actor: actor, Subject: events.Subject{Kind: "loop_run", ID: run.ID}, SubjectVersion: 1, Data: map[string]any{"loop_contract_id": contract.ID.String(), "reason": reason, "state": run.State}})
	if e != nil {
		return e
	}
	if event == "loop_run.escalated" {
		if m.reportNotifier == nil {
			return ErrUnavailable
		}
		_, e = m.reportNotifier.Notify(ctx, tx, notification.Notification{TenantID: contract.TenantID, ProjectID: loopProject(contract), Recipients: []guid.ID{contract.OwnerPrincipalID}, Type: "planning.loop_escalated", Severity: notification.Action, TemplateKey: "planning.loop_escalated", Permission: "report_read", LinkPath: "/admin/" + contract.TenantID.String() + "/agents?agent=" + contract.AgentID.String(), DedupeKey: "planning:loop-escalated:" + contract.ID.String(), Params: map[string]string{"loop_name": contract.Name, "reason": reason, "run_id": run.ID.String()}})
		if e != nil {
			return e
		}
		if autoPaused {
			recipients := []guid.ID{contract.OwnerPrincipalID}
			if m.agents.ByPrincipalOn == nil {
				return ErrUnavailable
			}
			agent, found, err := m.agents.ByPrincipalOn(ctx, tx, contract.TenantID, contract.AgentID)
			if err != nil {
				return err
			}
			if !found {
				return ErrUnavailable
			}
			if agent.OwnerPrincipalID > 0 {
				recipients = append(recipients, agent.OwnerPrincipalID)
			}
			_, e = m.reportNotifier.Notify(ctx, tx, notification.Notification{TenantID: contract.TenantID, ProjectID: loopProject(contract), Recipients: recipients, Type: "planning.loop_paused", Severity: notification.Action, TemplateKey: "planning.loop_paused", Permission: "report_read", LinkPath: "/admin/" + contract.TenantID.String() + "/agents?agent=" + contract.AgentID.String(), DedupeKey: "planning:loop-paused:" + run.ID.String(), Params: map[string]string{"loop_name": contract.Name, "reason": reason, "run_id": run.ID.String()}})
			if e != nil {
				return e
			}
			if e = m.recordLoop(ctx, tx, contract, "AUTO_PAUSED"); e != nil {
				return e
			}
		}
	}
	return nil
}
func (m *Module) recordLoopResultHTTP(ctx context.Context, in *LoopResultInput) (*respx.Body[LoopRunView], error) {
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	db, e := m.database()
	if e != nil {
		return nil, planningError(ctx, e)
	}
	if m.replay == nil {
		return nil, planningError(ctx, ErrUnavailable)
	}
	hash, e := idempotency.HashRequest("POST", "planning.loop.result:"+in.ID, nil, in.Body)
	if e != nil {
		return nil, planningError(ctx, e)
	}
	var out LoopRunView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		contract, run, e := m.authorizeLoopResultOn(ctx, tx, tenant, id)
		if e != nil {
			return e
		}
		result, _, e := m.replay.Do(ctx, tx, idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "planning.loop.result:" + in.ID, Value: in.Key, RequestHash: hash}, func(work *gorm.DB) (idempotency.Result, error) {
			view, err := m.completeLoopOn(ctx, work, contract, run, in)
			if err != nil {
				return idempotency.Result{}, err
			}
			raw, err := json.Marshal(view)
			return idempotency.Result{StatusCode: 200, Body: raw}, err
		})
		if e != nil {
			return e
		}
		return json.Unmarshal(result.Body, &out)
	})
	if e != nil {
		return nil, planningError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) loopTimeoutJobHandler() jobs.Handler {
	return jobs.Handler{Kind: "planning.loop_run_timeout", Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error { return m.scanLoopTimeouts(ctx, job.TenantID) }}
}
func (*Module) loopTimeoutJobSchedule() jobs.Schedule {
	return jobs.Schedule{Name: "planning.loop_run_timeout", Kind: "planning.loop_run_timeout", Interval: time.Minute, Timezone: "UTC", TenantScope: jobs.ScopePerTenant, Misfire: jobs.MisfireCoalesce, Payload: map[string]any{}}
}
func (m *Module) scanLoopTimeouts(ctx context.Context, tenant guid.ID) error {
	if tenant <= 0 {
		return ErrInvalid
	}
	if !m.loopRuntimeAvailable() {
		return ErrUnavailable
	}
	db, e := m.database()
	if e != nil {
		return e
	}
	q := query.BindLoopRuns(db, m.names)
	candidates, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.Eq("OPEN")).Order(q.OpenedAt, q.ID).Limit(500).Find()
	if e != nil {
		return e
	}
	for _, candidate := range candidates {
		e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			c := query.BindLoopContracts(tx, m.names)
			ref, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(candidate.LoopContractID)).Take()
			if err != nil {
				return err
			}
			if err = m.lockTeamLoopParticipantsOn(ctx, tx, tenant, ref); err != nil {
				return err
			}
			if m.principals.LockActiveRow == nil {
				return ErrUnavailable
			}
			if _, _, err = m.principals.LockActiveRow(ctx, tx, ref.AgentID); err != nil {
				return err
			}
			if ref.ProjectID != nil {
				if m.loopRuntime.projects.LockOn == nil {
					return ErrUnavailable
				}
				if _, err = m.loopRuntime.projects.LockOn(ctx, tx, tenant, *ref.ProjectID); err != nil {
					return err
				}
			}
			contract, err := m.loopOn(ctx, tx, tenant, ref.ID)
			if err != nil {
				return err
			}
			definition, err := loopView(contract)
			if err != nil {
				return err
			}
			q := query.BindLoopRuns(tx, m.names)
			run, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(candidate.ID)).Take()
			if err != nil {
				return err
			}
			if run.State != "OPEN" || m.now().Before(run.OpenedAt.Add(time.Duration(definition.Budget.MaxDurationMinutes)*time.Minute)) {
				return nil
			}
			// A missing settlement must not suppress a real timeout. Cost remains undisclosed.
			cost, err := m.loopCostOn(ctx, tx, contract, run.OpenedAt, m.now())
			if err != nil && !errors.Is(err, ErrUnavailable) {
				return err
			}
			if err == nil {
				run.CostMicros = cost
			}
			now := m.now()
			reason := "TIMED_OUT"
			run.State = "TIMED_OUT"
			run.CompletedAt = &now
			run.EscalationReason = &reason
			monthly := false
			if err == nil {
				monthly, err = m.loopMonthlyExceededOn(ctx, tx, contract, run, definition, cost, now)
				if err != nil && !errors.Is(err, ErrUnavailable) {
					return err
				}
			}
			return m.persistLoopCompletionOn(ctx, tx, contract, run, definition, monthly)
		})
		if e != nil {
			return e
		}
	}
	return nil
}

// availableLoopView exposes only capabilities actually wired into this process.
func (m *Module) availableLoopView(r *model.LoopContract) (LoopContractView, error) {
	v, e := loopView(r)
	if e != nil || !m.loopRuntimeAvailable() || (r.ProjectID == nil && m.loopRuntime.executions.AgentOn == nil) {
		return v, e
	}
	v.UnavailableCapabilities = []string{}
	return v, nil
}

func (m *Module) loopMonthlyExceededOn(ctx context.Context, tx *gorm.DB, contract *model.LoopContract, run *model.LoopRun, definition LoopContractView, cost int64, now time.Time) (bool, error) {
	if definition.Budget.MonthlyCostCapMicros == nil {
		return false, nil
	}
	settings, err := m.readSettingsOn(ctx, tx, contract.TenantID)
	if err != nil {
		return false, err
	}
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return false, err
	}
	local := now.In(loc)
	month := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	next := month.AddDate(0, 1, 0)
	q := query.BindLoopRuns(tx, m.names)
	prior, err := q.WithContext(ctx).Where(q.TenantID.Eq(contract.TenantID), q.LoopContractID.Eq(contract.ID), q.OpenedAt.Gte(month), q.OpenedAt.Lt(next), q.ID.Neq(run.ID), q.State.NotIn("SKIPPED", "FAILED")).Find()
	if err != nil {
		return false, err
	}
	total := int64(0)
	if !run.OpenedAt.Before(month) && run.OpenedAt.Before(next) {
		total = cost
	}
	for _, other := range prior {
		until := now
		if other.CompletedAt != nil {
			until = *other.CompletedAt
		}
		amount, err := m.loopCostOn(ctx, tx, contract, other.OpenedAt, until)
		if err != nil {
			return false, err
		}
		if total > math.MaxInt64-amount {
			return false, ErrInvalid
		}
		total += amount
	}
	return total > *definition.Budget.MonthlyCostCapMicros, nil
}
