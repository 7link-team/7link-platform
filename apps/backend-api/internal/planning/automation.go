package planning

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/internal/yunxiao"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) injectAutomationReportWriter(s *module.Services) error {
	connector, err := module.Resolve[yunxiao.AutomationReportWriterConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrUnavailable
	}
	return connector.Bind(m.writeAutomationReportOn)
}

// writeAutomationReportOn adds only actual current work facts. Automation cannot
// submit, adopt, confirm or reopen reports, or write arbitrary completion prose.
func (m *Module) writeAutomationReportOn(ctx context.Context, tx *gorm.DB, in yunxiao.AutomationReportEntry) error {
	if tx == nil || tx.Statement == nil || in.TenantID <= 0 || in.ProjectID <= 0 || in.RuleID <= 0 || in.RunID <= 0 || in.OccurredAt.IsZero() || in.OccurredAt.After(m.now()) || in.WorkItemID <= 0 || in.CreatorID <= 0 || in.PrincipalID <= 0 || in.RunAs != "SYSTEM:automation:"+in.RuleID.String() || !slices.Contains([]string{"completed", "uncompleted"}, in.Section) {
		return ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	if authn.PrincipalIDFromContext(ctx) != 0 || authn.TenantIDFromContext(ctx) != in.TenantID {
		return ErrForbidden
	}
	if m.members == nil || m.principals.LockActiveRow == nil || m.entityPolicy == nil || m.scheduledFacts.ReadOn == nil || m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	for _, principal := range slices.Compact(slices.Sorted(slices.Values([]guid.ID{in.CreatorID, in.PrincipalID}))) {
		p, active, err := m.principals.LockActiveRow(ctx, tx, principal)
		if err != nil {
			return err
		}
		if !active || p.ID != principal {
			return ErrForbidden
		}
		active, err = m.members.IsMemberActiveOn(ctx, tx, in.TenantID, principal)
		if err != nil {
			return err
		}
		if !active {
			return ErrForbidden
		}
	}
	permission, err := m.entityPolicy(ctx, tx, in.TenantID, in.ProjectID, "report_submit", in.CreatorID)
	if err != nil {
		return err
	}
	if !permission.AllowsEntityRows(in.ProjectID) || !(permission.AllowAll || slices.Contains(permission.ResourceIDs, in.ProjectID.String()) || (in.CreatorID == in.PrincipalID && slices.Contains(permission.OwnerIDs, in.CreatorID.String()))) {
		return ErrForbidden
	}
	q := query.BindReports(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(in.TenantID), q.PrincipalID.Eq(in.PrincipalID), q.ProjectID.In(guid.ID(0), in.ProjectID), q.State.Eq("DRAFT")).Order(q.ID).Limit(501).Find()
	if err != nil {
		return err
	}
	if len(rows) > 500 {
		return ErrUnavailable
	}
	found := false
	for _, candidate := range rows {
		start, end, err := reportPeriod(candidate)
		if err != nil {
			return err
		}
		if in.OccurredAt.Before(start) || !in.OccurredAt.Before(end) {
			continue
		}
		row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(in.TenantID), q.ID.Eq(candidate.ID)).First()
		if err != nil {
			return err
		}
		if row.State != "DRAFT" || row.PrincipalID != in.PrincipalID || (row.ProjectID != 0 && row.ProjectID != in.ProjectID) {
			continue
		}
		// The durable-trigger lookup may already have established a MySQL RR
		// snapshot. Carry a current locking read into the provider's GORM query
		// so a later ownership/state/acceptance change cannot become completion.
		facts, err := m.scheduledFacts.ReadOn(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), in.TenantID, in.ProjectID, in.PrincipalID, start, end)
		if err != nil {
			return err
		}
		var fact *delivery.PlanningWorkFact
		for i := range facts.Work {
			if facts.Work[i].ID == in.WorkItemID && facts.Work[i].AccountablePrincipalID == in.PrincipalID {
				fact = &facts.Work[i]
				break
			}
		}
		if fact == nil {
			return ErrNotFound
		}
		var body PlanningReportInput
		if err := json.Unmarshal([]byte(row.Content), &body); err != nil {
			return err
		}
		found = true
		if in.Section == "completed" {
			if fact.State != "ACCEPTED" || fact.AcceptedAt == nil || fact.AcceptedAt.Before(start) || !fact.AcceptedAt.Before(end) || fact.AcceptedAt.After(m.now()) {
				return ErrConflict
			}
			if slices.ContainsFunc(body.Completed, func(c PlanningCompletion) bool { return c.WorkItemID == fact.ID }) {
				continue
			}
			if len(body.Completed) >= 500 {
				return ErrInvalid
			}
			body.Completed = append(body.Completed, PlanningCompletion{WorkItemID: fact.ID})
		} else {
			if !slices.Contains([]string{"READY", "CLAIMED", "IN_PROGRESS", "REWORK", "BLOCKED", "NEEDS_HUMAN_REVIEW"}, fact.State) {
				return ErrConflict
			}
			if slices.ContainsFunc(body.UncompletedItems, func(c PlanningUncompleted) bool { return c.Item.WorkItemID != nil && *c.Item.WorkItemID == fact.ID }) {
				continue
			}
			if len(body.UncompletedItems) >= 100 {
				return ErrInvalid
			}
			id := fact.ID
			reason := "OTHER"
			if fact.State == "BLOCKED" {
				reason = "BLOCKED_DEPENDENCY"
			}
			if fact.State == "REWORK" {
				reason = "QUALITY_REWORK"
			}
			key := "automation:" + in.RuleID.String() + ":" + id.String()
			if slices.ContainsFunc(body.UncompletedItems, func(c PlanningUncompleted) bool { return c.Key == key }) {
				return ErrConflict
			}
			seed := []PlanningPlanItem{{Title: fact.Title, WorkItemID: &id}}
			if err := m.assignPlanItemIDs(seed, nil); err != nil {
				return err
			}
			body.UncompletedItems = append(body.UncompletedItems, PlanningUncompleted{Key: key, Reason: reason, Detail: fact.State, CarryOver: "CARRY_TO_NEXT", Item: seed[0]})
		}
		content, hash, err := hashJSON(body)
		if err != nil {
			return err
		}
		if len(content) > 1048576 {
			return ErrInvalid
		}
		result, err := q.WithContext(ctx).Where(q.TenantID.Eq(in.TenantID), q.ID.Eq(row.ID), q.Version.Eq(row.Version), q.State.Eq("DRAFT")).Updates(map[string]any{"content": content, "content_hash": hash, "last_check_hash": "", "needs_recheck": true, "version": row.Version + 1, "updated_at": m.now()})
		if err != nil {
			return err
		}
		if result.RowsAffected != 1 {
			return ErrConflict
		}
		if err := m.audit(ctx, tx, audit.DomainEvent{TenantID: in.TenantID, EventType: "planning.changed", TargetType: "planning_record", TargetID: row.ID, Detail: map[string]any{"authority_source": in.RunAs, "actor_kind": "SYSTEM", "action": "ADD_REPORT_ENTRY", "rule_id": in.RuleID, "run_id": in.RunID, "work_item_id": in.WorkItemID, "section": in.Section}}); err != nil {
			return err
		}
		_, err = m.events.Publish(ctx, tx, events.Event{TenantID: in.TenantID, ProjectID: row.ProjectID, Type: "planning.changed", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: in.RunAs}, Subject: events.Subject{Kind: "planning_record", ID: row.ID}, SubjectVersion: int64(row.Version + 1), Data: map[string]any{"kind": "report", "action": "ADD_REPORT_ENTRY", "rule_id": in.RuleID, "run_id": in.RunID, "work_item_id": in.WorkItemID, "section": in.Section, "content_hash": hash}})
		if err != nil {
			return err
		}
	}
	if !found {
		return ErrNotFound
	}
	return nil
}
