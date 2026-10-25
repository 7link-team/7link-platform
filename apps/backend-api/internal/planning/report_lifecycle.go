package planning

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrStaleCheck = errors.New("planning: report requires another reconciliation")

func (m *Module) injectReportLifecycle(s *module.Services) error {
	var err error
	m.reportSuperior, err = module.Resolve[delivery.ReportSuperior](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	catalog, err := module.Resolve[events.SubscriberCatalog](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return catalog.AddSubscribers(Name, events.Subscriber{Name: "planning.recheck_on_change", Mode: events.Transactional, StartFrom: events.Earliest, Partitions: 1, EventTypes: []string{"work_item.*", "decision.*", "delivery.*", "acceptance.*", "project.stage_changed", "execution.cost_updated", "agent.*", "planning.changed"}, Handle: m.recheckReportsOn})
}

// Confirmation is a relationship, in addition to current IAM permission.
func (m *Module) reportConfirmerOn(ctx context.Context, tx *gorm.DB, r *model.Report) (guid.ID, error) {
	subject := reportAuthor(r)
	if subject > 0 && m.agents.ByPrincipalOn != nil {
		a, found, err := m.agents.ByPrincipalOn(ctx, tx, r.TenantID, subject)
		if err != nil {
			return 0, err
		}
		if found && a.TenantID == r.TenantID && a.PrincipalID == subject && a.State == "ACTIVE" && a.OwnerPrincipalID > 0 {
			return m.activeReportHumanOn(ctx, tx, r.TenantID, a.OwnerPrincipalID)
		}
	}
	if subject > 0 && m.reportSuperior != nil {
		id, err := m.reportSuperior(ctx, tx, r.TenantID, subject)
		if err != nil {
			return 0, err
		}
		if id > 0 && id != subject {
			return m.activeReportHumanOn(ctx, tx, r.TenantID, id)
		}
	}
	settings, err := m.readSettingsOn(ctx, tx, r.TenantID)
	if err != nil {
		return 0, err
	}
	if settings.TeamOwnerID == subject {
		return 0, nil
	}
	return m.activeReportHumanOn(ctx, tx, r.TenantID, settings.TeamOwnerID)
}
func (m *Module) activeReportHumanOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (guid.ID, error) {
	if id <= 0 {
		return 0, nil
	}
	p, active, err := m.principals.ActiveHumanByID(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	if !active || p.ID != id {
		return 0, nil
	}
	member, err := m.members.IsMemberActiveOn(ctx, tx, tenant, id)
	if err != nil || !member {
		return 0, err
	}
	return id, nil
}

// Reopening does not grant editing authority. The recorded confirmer or current
// team owner must still hold current report_confirm permission as a Human.
func (m *Module) authorizeReportReopenOn(ctx context.Context, tx *gorm.DB, r *model.Report, confirmedBy *guid.ID) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if actor == reportAuthor(r) {
		return ErrForbidden
	}
	if confirmedBy != nil && *confirmedBy == actor {
		return nil
	}
	q := query.BindSettings(tx, m.names)
	settings, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(r.TenantID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if actor != settings.TeamOwnerID {
		return ErrForbidden
	}
	return nil
}

// Source-change delivery is receipt-deduplicated by the transactional event
// subscriber. No user authority is impersonated by this system invalidation.
func (m *Module) recheckReportsOn(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
	if e.TenantID <= 0 || e.EventID <= 0 || tx == nil {
		return ErrInvalid
	}
	if e.EventType == "planning.changed" {
		var d struct{ Kind, Action string }
		if json.Unmarshal(e.Data, &d) != nil {
			return ErrInvalid
		}
		if d.Kind != "report" || d.Action == "RECHECK" {
			return nil
		}
	}
	var data map[string]json.RawMessage
	if len(e.Data) > 0 && json.Unmarshal(e.Data, &data) != nil {
		return ErrInvalid
	}
	workID := e.Subject.ID
	if raw := data["work_item_id"]; len(raw) > 0 {
		var id guid.ID
		if json.Unmarshal(raw, &id) == nil && id > 0 {
			workID = id
		}
	}
	q := query.BindReports(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.State.In("READY", "SUBMITTED"), q.NeedsRecheck.Is(false)).Order(q.ID).Clauses(clause.Locking{Strength: "UPDATE"}).Find()
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.ID == e.Subject.ID && e.EventType == "planning.changed" {
			continue
		}
		if e.OccurredAt.Before(r.UpdatedAt) {
			continue
		}
		affected := false
		if strings.HasPrefix(e.EventType, "agent.") && (r.PrincipalID == e.Subject.ID || r.PrincipalID == workID) {
			affected = true
		}
		if e.EventType == "planning.changed" {
			var body PlanningReportInput
			if json.Unmarshal([]byte(r.Content), &body) != nil {
				return ErrInvalid
			}
			affected = slices.ContainsFunc(body.Children, func(c PlanningChildReportRef) bool { return c.ID == e.Subject.ID })
		} else if e.ProjectID > 0 && (r.ProjectID == e.ProjectID || r.ProjectID == 0) {
			var facts delivery.PlanningSnapshot
			if json.Unmarshal([]byte(r.Facts), &facts) != nil {
				return ErrInvalid
			}
			affected = slices.ContainsFunc(facts.Work, func(w delivery.PlanningWorkFact) bool { return w.ID == workID })
			if e.EventType == "execution.cost_updated" || e.EventType == "project.stage_changed" || e.EventType == "delivery.sent" || e.EventType == "acceptance.received" {
				affected = true
			}
			var body PlanningReportInput
			if json.Unmarshal([]byte(r.Content), &body) != nil {
				return ErrInvalid
			}
			affected = affected || slices.ContainsFunc(body.Completed, func(c PlanningCompletion) bool { return c.WorkItemID == workID })
		}
		if !affected {
			continue
		}
		_, err = q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.ID.Eq(r.ID)).Updates(map[string]any{"needs_recheck": true, "last_check_hash": "", "version": r.Version + 1, "updated_at": m.now()})
		if err != nil {
			return err
		}
		r.NeedsRecheck = true
		r.LastCheckHash = ""
		r.Version++
		v, err := reportView(r)
		if err != nil {
			return err
		}
		system := authn.WithClaims(ctx, &authn.Claims{TenantID: e.TenantID})
		if err = m.record(system, tx, e.TenantID, v, "SYSTEM", "RECHECK", "", map[string]any{"source_event_id": e.EventID.String()}); err != nil {
			return err
		}
	}
	return nil
}
