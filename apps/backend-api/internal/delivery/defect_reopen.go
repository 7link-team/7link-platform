package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"slices"
	"time"
)

type defectReopenBody struct {
	SeenSeq *int64 `json:"seen_seq,omitempty" minimum:"0"`
	Reason  string `json:"reason" minLength:"5" maxLength:"2000"`
}

// Reopening creates a new canonical BUG. The accepted/canceled source and its
// audit history remain immutable. Client rejection retains its separate T26.
func (m *Module) reopenDefectOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor, owner guid.ID, expected int, in defectReopenBody) (WorkView, error) {
	var out WorkView
	reason, err := resultText(in.Reason)
	if err != nil || len([]rune(reason)) < 5 {
		return out, ErrInvalidArgument
	}
	_, source, _, err := m.lockedWorkActorOn(ctx, tx, tenant, project, id, actor, owner, "work_item_write", true)
	if err != nil {
		return out, err
	}
	if source.Version != expected {
		return out, ErrWorkVersion
	}
	if !slices.Contains([]string{"BUG", "INCIDENT"}, source.Type) || !slices.Contains([]string{"ACCEPTED", "CANCELLED"}, source.State) { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return out, ErrDefectReopen
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return out, err
	}
	kind := "HUMAN"
	if !active || human.ID != actor {
		if source.State != "ACCEPTED" {
			return out, ErrWorkForbidden
		}
		if err = m.defectRoleOn(ctx, tx, tenant, project, actor, "agent_tester"); err != nil {
			return out, err
		}
		kind = "AGENT"
		if in.SeenSeq == nil {
			return out, ErrWorkSeenSeqRequired
		}
		if err = m.checkWorkSeenOn(ctx, tx, source, in.SeenSeq); err != nil {
			return out, err
		}
	}
	criteria := query.BindWorkCriteria(tx, m.service.names)
	previous, err := criteria.WithContext(ctx).Where(criteria.TenantID.Eq(tenant), criteria.ProjectID.Eq(project), criteria.WorkItemID.Eq(id)).Order(criteria.Position).Find()
	if err != nil {
		return out, err
	}
	input := workCreateBody{Title: source.Title, Description: source.Description, Type: "BUG", AccountablePrincipalID: owner, Severity: source.Severity, Priority: source.Priority, Reproducibility: source.Reproducibility, FoundVersion: source.FoundVersion}
	if source.Objective != nil {
		input.Objective = *source.Objective
	}
	for _, c := range previous {
		input.Criteria = append(input.Criteria, workCriterionInput{Text: c.Text, VerificationMethod: c.VerificationMethod})
	}
	if source.DefectDetails != nil {
		var details DefectDescription
		if json.Unmarshal([]byte(*source.DefectDetails), &details) != nil {
			return out, ErrUnavailable
		}
		details.Resolution = nil
		details.DuplicateOfWorkItemID = nil
		input.DefectDetails = &details
	}
	created, err := m.createWorkWithActorOn(ctx, tx, tenant, project, actor, input, kind == "AGENT")
	if err != nil {
		return out, err
	}
	stage := (*string)(nil)
	if kind == "HUMAN" && source.State == "ACCEPTED" {
		value := "HUMAN_REVIEW"
		stage = &value
	}
	w := query.BindWorkItems(tx, m.service.names)
	if _, err = w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(created.ID)).UpdateSimple(w.ReopenedFromWorkItemID.Value(id)); err != nil {
		return out, err
	}
	if source.Module != nil {
		if _, err = w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(created.ID)).UpdateSimple(w.Module.Value(*source.Module)); err != nil {
			return out, err
		}
	}
	if stage != nil {
		if _, err = w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(created.ID)).UpdateSimple(w.EscapedFromStage.Value(*stage)); err != nil {
			return out, err
		}
		if source.AcceptedDecisionID == nil || source.CurrentClaimID == nil {
			return out, ErrResultState
		}
		receipt := query.BindReportQualityReceipts(tx, m.service.names)
		n, e := receipt.WithContext(ctx).Where(receipt.TenantID.Eq(tenant), receipt.WorkItemID.Eq(id), receipt.Kind.Eq("ESCAPE_HUMAN"), receipt.SourceID.Eq(*source.AcceptedDecisionID)).Count()
		if e != nil {
			return out, e
		}
		if n == 0 {
			if err = m.recordReportQualityOn(ctx, tx, tenant, project, id, "ESCAPE_HUMAN", *source.AcceptedDecisionID, 0, time.Now().UTC(), *source.CurrentClaimID); err != nil {
				return out, err
			}
			if _, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "quality.escaped", Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: "permission:work_item_write"}, Subject: events.Subject{Kind: "work_item", ID: id}, Data: map[string]any{"escape_key": id.String() + ":" + source.AcceptedDecisionID.String(), "work_item_id": id, "reopened_work_item_id": created.ID, "stage": *stage, "category": "FUNCTIONAL_DEFECT", "summary": reason, "decision_ref": *source.AcceptedDecisionID}}); err != nil {
				return out, err
			}
		}
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.defect_reopened", "work_item", created.ID)
	event.Detail["reopened_from_work_item_id"] = id.String()
	event.Detail["reason"] = reason
	event.Detail["actor_kind"] = kind
	if err = m.service.audit(ctx, tx, event); err != nil {
		return out, err
	}
	created.ReopenedFromWorkItemID = &id
	created.EscapedFromStage = stage
	return created.WorkView, nil
}
