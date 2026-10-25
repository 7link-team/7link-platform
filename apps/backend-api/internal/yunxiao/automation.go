package yunxiao

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AutomationCommands is an in-process execution boundary. No public endpoint
// accepts caller-asserted outcomes. An executor must call ExecuteOn with the real
// domain action; unavailable executors must remain unavailable.
type AutomationCommands struct {
	RegisterOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, string, bool) (guid.ID, error)
	ExecuteOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, func(context.Context, *gorm.DB) error) (guid.ID, error)
}

type AutomationFailureFact struct {
	ID, ProjectID, RuleID, CreatorID, WorkItemID guid.ID
	RuleName, Error                              string
}
type AutomationNotifications struct {
	RunOn      func(context.Context, *gorm.DB, guid.ID, guid.ID) (AutomationFailureFact, bool, error)
	EligibleOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
}

func (m *Module) provideAutomation(s *module.Services) error {
	if err := m.provideAutomationDispatcher(s); err != nil {
		return err
	}
	if err := module.Provide[AutomationCommands](s, AutomationCommands{RegisterOn: m.registerAutomationRuleOn, ExecuteOn: m.executeAutomationOn}); err != nil {
		return err
	}
	return module.Provide[AutomationNotifications](s, AutomationNotifications{RunOn: m.automationFailureOn, EligibleOn: m.automationEligibleOn})
}

// registerAutomationRuleOn stores identity and optimistic version only; it does
// not claim that a trigger/action engine has been installed.
func (m *Module) registerAutomationRuleOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version int, name string, enabled bool) (guid.ID, error) {
	if err := m.projectAuthorityOn(ctx, tx, tenant, project, "workflow_manage"); err != nil {
		return 0, err
	}
	if strings.TrimSpace(name) == "" || !textValid(name, 80) {
		return 0, ErrInvalid
	}
	q := query.BindAutomationRules(tx, m.names)
	now := m.now()
	row := &model.AutomationRule{TenantID: tenant, ProjectID: project, Name: strings.TrimSpace(name), Enabled: enabled, Version: 1, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: now, UpdatedAt: now}
	if id == 0 {
		if version != 0 {
			return 0, ErrConflict
		}
		var err error
		row.ID, err = m.nextID()
		if err != nil {
			return 0, err
		}
		if err = q.WithContext(ctx).Create(row); err != nil {
			return 0, err
		}
	} else {
		old, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrNotFound
		}
		if err != nil {
			return 0, err
		}
		if version < 1 || old.Version != version {
			return 0, ErrConflict
		}
		row.ID = id
		row.CreatedBy = old.CreatedBy
		row.CreatedAt = old.CreatedAt
		row.Version = version + 1
		result, err := q.WithContext(ctx).Where(q.ID.Eq(id), q.TenantID.Eq(tenant), q.Version.Eq(version)).Select(q.Name, q.Enabled, q.Version, q.UpdatedAt).Updates(row)
		if err != nil {
			return 0, err
		}
		if result.RowsAffected != 1 {
			return 0, ErrConflict
		}
	}
	if m.audit == nil || m.events == nil {
		return 0, ErrUnavailable
	}
	if err := m.audit(ctx, tx, audit.NewDomainEvent(ctx, tenant, "automation.rule_registered", "automation_rule", row.ID)); err != nil {
		return 0, err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "automation.rule_registered", Actor: events.Actor{Kind: "HUMAN", ID: authn.ActorIDFromContext(ctx), AuthoritySource: "IAM"}, Subject: events.Subject{Kind: "automation_rule", ID: row.ID}, SubjectVersion: int64(row.Version), Data: map[string]any{"enabled": enabled}})
	return row.ID, err
}

// The locked rule serializes executions and replay checks. Action failure rolls
// its writes back to the savepoint; the immutable failure, audit, and outbox are
// committed together by the caller. Returning nil on action failure is deliberate:
// the outcome is a persisted fact, not an instruction to retry the action.
func (m *Module) executeAutomationOn(ctx context.Context, tx *gorm.DB, tenant, rule, trigger, work guid.ID, action func(context.Context, *gorm.DB) error) (guid.ID, error) {
	if tx == nil || action == nil || tenant <= 0 || rule <= 0 || trigger <= 0 || work <= 0 {
		return 0, ErrInvalid
	}
	if authn.TenantIDFromContext(ctx) != tenant {
		return 0, ErrForbidden
	}
	if tx.Statement == nil {
		return 0, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalid
	}
	q := query.BindAutomationRules(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(rule)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if err = m.projectAuthorityOn(ctx, tx, tenant, r.ProjectID, "workflow_manage"); err != nil {
		return 0, err
	}
	// Preserve the shared principal -> project -> domain-row lock order.
	r, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(rule)).First()
	if err != nil {
		return 0, err
	}
	if m.workItemOn == nil || m.audit == nil || m.events == nil || m.automationEvents == nil {
		return 0, ErrUnavailable
	}
	if err = m.workItemOn(ctx, tx, tenant, r.ProjectID, work); err != nil {
		return 0, err
	}
	source, err := m.automationEvents(ctx, tx, trigger-1, 1)
	if err != nil {
		return 0, err
	}
	if len(source) != 1 || source[0].ID != trigger || source[0].TenantID != tenant || source[0].ProjectID != r.ProjectID || source[0].SubjectID != work || source[0].SubjectKind != "work_item" {
		return 0, ErrInvalid
	}
	switch source[0].Type {
	case "work_item.state_changed", "work_item.created", "work_item.assigned":
	default:
		return 0, ErrInvalid
	}
	runs := query.BindAutomationRuns(tx, m.names)
	old, err := runs.WithContext(ctx).Where(runs.TenantID.Eq(tenant), runs.RuleID.Eq(rule), runs.TriggerEventID.Eq(trigger), runs.WorkItemID.Eq(work)).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	if old != nil {
		return old.ID, nil
	}
	id, err := m.nextID()
	if err != nil {
		return 0, err
	}
	started := m.now()
	receipt := &model.AutomationRun{ID: id, TenantID: tenant, ProjectID: r.ProjectID, RuleID: rule, RuleVersion: r.Version, RuleName: r.Name, CreatedBy: r.CreatedBy, TriggerEventID: trigger, WorkItemID: work, Outcome: "SKIPPED", CreatedAt: started}
	receipt.DefinitionSnapshot = r.Definition
	eligible := r.Enabled && r.ArchivedAt == nil
	if r.Definition != "" {
		var def AutomationDefinition
		if json.Unmarshal([]byte(r.Definition), &def) != nil {
			return 0, ErrUnavailable
		}
		matched, reason, _, e := automationDefinitionMatches(r, source[0])
		if e != nil {
			return 0, e
		}
		eligible = eligible && matched
		if !matched {
			receipt.Error = reason
		}
		var count int64
		hour := started.UTC().Truncate(time.Hour)
		if e := tx.WithContext(ctx).Table(m.names.AutomationRuns.String()).Where("tenant_id = ? AND rule_id = ? AND created_at >= ? AND created_at < ? AND outcome <> ?", tenant, rule, hour, hour.Add(time.Hour), "SKIPPED").Count(&count).Error; e != nil {
			return 0, e
		}
		if count >= int64(def.MaxRunsPerHour) {
			eligible = false
			receipt.Error = "HOURLY_LIMIT"
		}
	}
	if eligible {
		if err = tx.SavePoint("automation_action").Error; err != nil {
			return 0, err
		}
		actionErr := action(ctx, tx)
		receipt.Outcome = "SUCCEEDED"
		if actionErr != nil {
			if err = tx.RollbackTo("automation_action").Error; err != nil {
				return 0, err
			}
			receipt.Outcome = "FAILED"
			// Arbitrary domain errors may contain SQL, tokens, or user text. The public
			// receipt reports a stable neutral failure instead of exposing internals.
			receipt.Error = "ACTION_FAILED"
		}
	}
	receipt.DurationMS = max(int64(0), m.now().Sub(started).Milliseconds())
	if err = runs.WithContext(ctx).Create(receipt); err != nil {
		return 0, err
	}
	if err = m.audit(ctx, tx, audit.NewDomainEvent(ctx, tenant, "automation.run_recorded", "automation_run", id)); err != nil {
		return 0, err
	}
	if receipt.Outcome == "FAILED" {
		_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: r.ProjectID, Type: "automation.run_failed", Actor: events.Actor{Kind: "HUMAN", ID: authn.ActorIDFromContext(ctx), AuthoritySource: "IAM"}, Subject: events.Subject{Kind: "automation_run", ID: id}, SubjectVersion: 1, Data: map[string]any{"rule_id": rule, "work_item_id": work}})
	}
	return id, err
}

func (m *Module) automationFailureOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (AutomationFailureFact, bool, error) {
	out := AutomationFailureFact{}
	if tx == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalid
	}
	q := query.BindAutomationRuns(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Outcome.Eq("FAILED")).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	return AutomationFailureFact{ID: row.ID, ProjectID: row.ProjectID, RuleID: row.RuleID, CreatorID: row.CreatedBy, WorkItemID: row.WorkItemID, RuleName: row.RuleName, Error: row.Error}, true, nil
}

func (m *Module) automationEligibleOn(ctx context.Context, tx *gorm.DB, tenant, run, actor guid.ID) (bool, error) {
	fact, found, err := m.automationFailureOn(ctx, tx, tenant, run)
	if err != nil || !found {
		return false, err
	}
	target := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: actor, EntityID: fact.ProjectID, SubjectType: authn.SubjectTypePrincipal})
	if err = m.projectAuthorityOn(target, tx, tenant, fact.ProjectID, "work_item_read"); err != nil {
		return false, automationAudienceError(err)
	}
	if actor == fact.CreatorID {
		return true, nil
	}
	err = m.projectAuthorityOn(target, tx, tenant, fact.ProjectID, "workflow_manage")
	if err != nil {
		return false, automationAudienceError(err)
	}
	return true, nil
}
func automationAudienceError(err error) error {
	if errors.Is(err, ErrForbidden) || errors.Is(err, delivery.ErrWorkForbidden) || errors.Is(err, delivery.ErrInvalidMember) || errors.Is(err, delivery.ErrProjectNotFound) || errors.Is(err, delivery.ErrWorkProjectReadOnly) {
		return nil
	}
	return err
}
