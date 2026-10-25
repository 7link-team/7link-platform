package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type workflowSelectionContextKey struct{}

type workTransitionBody struct {
	ToStateKey string                     `json:"to_state_key" minLength:"2" maxLength:"40" pattern:"^[a-z][a-z0-9_]{1,39}$"`
	Fields     map[string]json.RawMessage `json:"fields,omitempty" maxProperties:"20"`
	Comment    string                     `json:"comment,omitempty" maxLength:"2000"`
	SeenSeq    *int64                     `json:"seen_seq,omitempty" minimum:"0"`
}

type workTransitionView struct {
	ID          guid.ID `json:"id"`
	WorkItemID  guid.ID `json:"work_item_id"`
	StateKey    string  `json:"state_key"`
	State       string  `json:"state"`
	Version     int     `json:"version"`
	ActivitySeq int64   `json:"activity_seq"`
}

func transitionPreparation(row *model.WorkItem, fields map[string]json.RawMessage) (*WorkPreparationBody, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	current, err := preparationRecord(row)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(current.WorkPreparationBody)
	if err != nil {
		return nil, err
	}
	var merged map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &merged); err != nil {
		return nil, err
	}
	preparationCount := 0
	for key, value := range fields {
		if slices.Contains(transitionCommandKeys, key) {
			continue
		}
		preparationCount++
		// These are the same editable inputs as work_item.prepare. Contract
		// confirmation remains an explicit Human command, never inferred here.
		if !slices.Contains(transitionPreparationKeys, key) {
			return nil, ErrInvalidArgument
		}
		merged[key] = value
	}
	if preparationCount == 0 {
		return nil, nil
	}
	encoded, err = json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	var out WorkPreparationBody
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&out); err != nil {
		return nil, ErrInvalidArgument
	}
	valid, err := validatePreparation(out)
	return &valid, err
}

// The latest canonical business receipt invalidates an older display selection.
// This uses the existing immutable transition provenance instead of a second
// mutable state store which could diverge from the Delivery aggregate.
func (m *Module) workDisplayKeyOn(ctx context.Context, tx *gorm.DB, row *model.WorkItem) (string, error) {
	q := query.BindWorkTransitions(tx, m.service.names)
	last, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.WorkItemID.Eq(row.ID)).Order(q.ID.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return m.resolveWorkflowDisplayKeyOn(ctx, tx, row, strings.ToLower(row.State))
	}
	if err != nil {
		return "", err
	}
	if last.ToState == row.State && last.ToStateKey != nil && *last.ToStateKey != "" {
		return m.resolveWorkflowDisplayKeyOn(ctx, tx, row, *last.ToStateKey)
	}
	return m.resolveWorkflowDisplayKeyOn(ctx, tx, row, strings.ToLower(row.State))
}

func (m *Module) transitionAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (*model.WorkItem, string, error) {
	actor := authn.PrincipalIDFromContext(ctx)
	if m == nil || m.service == nil || !isWorkActivityTransaction(tx) {
		return nil, "", ErrUnavailable
	}
	q := query.BindWorkItems(tx, m.service.names)
	snapshot, err := q.WithContext(ctx).Select(q.AccountablePrincipalID).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", ErrProjectNotFound
	}
	if err != nil {
		return nil, "", err
	}
	_, row, _, err := m.lockedWorkActorOn(ctx, tx, tenant, project, id, actor, snapshot.AccountablePrincipalID, "work_item_write", true)
	if err != nil {
		return nil, "", err
	}
	kind, err := m.resultActorKindOn(ctx, tx, actor, "")
	return row, kind, err
}

func (m *Module) applyWorkTransitionOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, in workTransitionBody) (workTransitionView, error) {
	var out workTransitionView
	// Canonical commands may perform work before a later workflow condition
	// rejects them. Roll that work back before the outer replay store caches an
	// ordinary business failure; interrupted writes escape that store entirely.
	err := tx.Transaction(func(work *gorm.DB) error {
		row, kind, err := m.transitionAuthorityOn(ctx, work, tenant, project, id)
		if err != nil {
			return err
		}
		if err = m.checkWorkContextOn(ctx, work, tenant, id, in.SeenSeq, kind == "AGENT"); err != nil {
			return err
		}
		if m.workflowSelection == nil {
			return ErrUnavailable
		}
		fromKey, err := m.workDisplayKeyOn(ctx, work, row)
		if err != nil {
			return err
		}
		actor := authn.PrincipalIDFromContext(ctx)
		preparation, err := transitionPreparation(row, in.Fields)
		if err != nil {
			return err
		}
		proposed := *row
		if preparation != nil {
			proposed.EstimateMinutes = preparation.EstimateMinutes
			proposed.Risk, proposed.AutonomyLevel = preparation.Risk, preparation.AutonomyLevel
		}
		request := WorkflowTransition{TenantID: tenant, ProjectID: project, WorkItemID: id, ActorID: actor, Type: row.Type, From: row.State, FromKey: fromKey, ToKey: in.ToStateKey, ActorKind: kind, Trigger: "COMMAND"}
		request.FactOn = func(k, rule, argument string) (bool, error) {
			return m.workflowFactOn(ctx, work, &proposed, actor, k, rule, argument)
		}
		to, err := m.workflowSelection(ctx, work, request)
		if err != nil {
			return err
		}
		request.To = to
		commandCtx := context.WithValue(ctx, workflowSelectionContextKey{}, request)
		before, expected := row.State, row.Version
		if to != before || preparation != nil {
			if err := m.workflowCommandOn(commandCtx, work, row, to, in, preparation); err != nil {
				return err
			}
		} else {
			if len(in.Fields) != 0 || slices.Contains([]string{"ACCEPTED", "CANCELLED"}, before) { //nolint:misspell // Preserve the published domain or serialized contract spelling.
				return ErrWorkPreparationState
			}
			w := query.BindWorkItems(work, m.service.names)
			changed, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(id), w.Version.Eq(expected)).Updates(map[string]any{"version": expected + 1, "updated_at": time.Now().UTC(), "updated_by": actor})
			if err != nil {
				return err
			}
			if changed.RowsAffected != 1 {
				return ErrWorkVersion
			}
			row.Version++
		}
		transitionID, err := m.ids.Next()
		if err != nil {
			return err
		}
		now, source := time.Now().UTC().Truncate(time.Millisecond), "display-transition:"+guid.ID(transitionID).String()
		receipt := &model.WorkItemTransition{TenantID: tenant, ID: guid.ID(transitionID), ProjectID: project, WorkItemID: id, FromState: &before, ToState: to, FromStateKey: &fromKey, ToStateKey: &in.ToStateKey, Comment: &in.Comment, Trigger: "COMMAND", ActorKind: kind, ActorID: &actor, AuthoritySource: "permission:work_item_write", ReasonCode: "DISPLAY_TRANSITIONED", SourceRef: &source, VersionAfter: row.Version, CreatedAt: now}
		if err = query.BindWorkTransitions(work, m.service.names).WithContext(commandCtx).Create(receipt); err != nil {
			return err
		}
		seq, err := m.recordWorkActivityOn(ctx, work, tenant, id, WorkActivityInput{Kind: "TRANSITIONED", ActorKind: kind, ActorID: actor, RefKind: "transition", RefID: receipt.ID, Title: "Work item display transition"})
		if err != nil {
			return err
		}
		a := audit.NewDomainEvent(ctx, tenant, "delivery.work_item_transitioned", "work_item", id)
		a.Detail["transition_id"], a.Detail["from_key"], a.Detail["to_key"] = receipt.ID.String(), fromKey, in.ToStateKey
		if err = m.service.audit(ctx, work, a); err != nil {
			return err
		}
		_, err = m.service.events.Publish(ctx, work, events.Event{TenantID: tenant, ProjectID: project, Type: "work_item.transitioned", Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: "permission:work_item_write"}, Subject: events.Subject{Kind: "work_item", ID: id}, SubjectVersion: int64(row.Version), Data: map[string]any{"transition_id": receipt.ID.String(), "from_state_key": fromKey, "to_state_key": in.ToStateKey}})
		if err != nil {
			return err
		}
		out = workTransitionView{ID: receipt.ID, WorkItemID: id, StateKey: in.ToStateKey, State: to, Version: row.Version, ActivitySeq: seq}
		return nil
	})
	return out, err
}
