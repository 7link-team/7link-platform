package governance

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// WorkflowDecisionRef identifies one frozen node, including its expansion path.
type WorkflowDecisionRef struct {
	TenantID       guid.ID `json:"tenant_id"`
	ProjectID      guid.ID `json:"project_id"`
	BindingID      guid.ID `json:"binding_id"`
	DefinitionHash string  `json:"definition_hash"`
	ExpansionKey   string  `json:"expansion_key"`
	Gate           string  `json:"gate"`
}
type WorkflowDecisionInput struct {
	WorkflowDecisionRef
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}
type WorkflowDecisionFact struct {
	ID guid.ID `json:"id"`
	WorkflowDecisionInput
	ActorID         guid.ID `json:"actor_id"`
	AuthoritySource string  `json:"authority_source"`
}

// Callers hold the current project and binding fences. No write is made by reads.
type WorkflowDecisions struct {
	RecordOn func(context.Context, *gorm.DB, WorkflowDecisionInput) (WorkflowDecisionFact, error)
	ReadOn   func(context.Context, *gorm.DB, WorkflowDecisionRef) (WorkflowDecisionFact, bool, error)
}

func validWorkflowDecisionRef(r WorkflowDecisionRef) bool {
	return r.TenantID > 0 && r.ProjectID > 0 && r.BindingID > 0 && len(r.DefinitionHash) == 64 && r.ExpansionKey != "" && utf8.RuneCountInString(r.ExpansionKey) <= 512 && (r.Gate == "TEAM" || r.Gate == "CLIENT")
}
func workflowDecisionFact(r *model.WorkflowDecision) WorkflowDecisionFact {
	return WorkflowDecisionFact{ID: r.ID, WorkflowDecisionInput: WorkflowDecisionInput{WorkflowDecisionRef: WorkflowDecisionRef{TenantID: r.TenantID, ProjectID: r.ProjectID, BindingID: r.BindingID, DefinitionHash: r.DefinitionHash, ExpansionKey: r.ExpansionKey, Gate: r.Gate}, Outcome: r.Outcome, Reason: r.Reason}, ActorID: r.ActorID, AuthoritySource: r.AuthoritySource}
}
func (m *Module) readWorkflowDecisionOn(ctx context.Context, tx *gorm.DB, ref WorkflowDecisionRef) (WorkflowDecisionFact, bool, error) {
	var out WorkflowDecisionFact
	if tx == nil || tx.Statement == nil || !validWorkflowDecisionRef(ref) || authn.TenantIDFromContext(ctx) != ref.TenantID {
		return out, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalidArgument
	}
	q := query.BindWorkflowDecisions(tx, m.seed.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(ref.TenantID), q.ProjectID.Eq(ref.ProjectID), q.BindingID.Eq(ref.BindingID), q.ExpansionKey.Eq(ref.ExpansionKey), q.DefinitionHash.Eq(ref.DefinitionHash), q.Gate.Eq(ref.Gate)).Take()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if e != nil {
		return out, false, e
	}
	return workflowDecisionFact(r), true, nil
}
func (m *Module) recordWorkflowDecisionOn(ctx context.Context, tx *gorm.DB, in WorkflowDecisionInput) (WorkflowDecisionFact, error) {
	var out WorkflowDecisionFact
	if !validWorkflowDecisionRef(in.WorkflowDecisionRef) || (in.Outcome != "APPROVE" && in.Outcome != "REJECT") || strings.TrimSpace(in.Reason) == "" || utf8.RuneCountInString(in.Reason) > 2000 {
		return out, ErrInvalidArgument
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if authn.TenantIDFromContext(ctx) != in.TenantID || actor <= 0 {
		return out, ErrForbidden
	}
	if m.principals.LockActiveRow == nil || m.principals.ActiveHumanByID == nil || m.explain == nil || m.seed.audit == nil || m.publisher == nil || m.seed.ids == nil {
		return out, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil {
		return out, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	if _, active, e := m.principals.LockActiveRow(ctx, tx, actor); e != nil {
		return out, e
	} else if !active {
		return out, ErrForbidden
	}
	if human, ok, e := m.principals.ActiveHumanByID(ctx, tx, actor); e != nil {
		return out, e
	} else if !ok || human.ID != actor {
		return out, ErrForbidden
	}
	permission := "workflow_gate_" + strings.ToLower(in.Gate)
	explanation, e := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: in.TenantID, EntityID: in.ProjectID, PrincipalID: actor, Permission: permission})
	if e != nil {
		return out, e
	}
	if !explanation.Allowed {
		return out, ErrForbidden
	}
	authority, e := m.seed.roleAuthoritySource(ctx, tx, in.TenantID, permission, explanation)
	if e != nil {
		return out, e
	}
	if authority.source == "" {
		return out, ErrForbidden
	}
	prior, ok, e := m.readWorkflowDecisionOn(ctx, tx, in.WorkflowDecisionRef)
	if e != nil {
		return out, e
	}
	if ok {
		if prior.Outcome != in.Outcome || prior.Reason != in.Reason || prior.ActorID != actor {
			return out, ErrVersion
		}
		return prior, nil
	}
	id, e := m.seed.ids.Next()
	if e != nil {
		return out, e
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	r := &model.WorkflowDecision{ID: guid.ID(id), TenantID: in.TenantID, ProjectID: in.ProjectID, BindingID: in.BindingID, DefinitionHash: in.DefinitionHash, ExpansionKey: in.ExpansionKey, Gate: in.Gate, Outcome: in.Outcome, Reason: in.Reason, ActorID: actor, AuthoritySource: authority.source, CreatedAt: m.seed.now()}
	if e = query.BindWorkflowDecisions(tx, m.seed.names).WithContext(ctx).Create(r); e != nil {
		return out, e
	}
	event := audit.NewDomainEvent(ctx, in.TenantID, "governance.workflow_decided", "workflow_decision", r.ID)
	event.Detail["authority_source"] = authority.source
	event.Detail["binding_id"] = in.BindingID.String()
	event.Detail["definition_hash"] = in.DefinitionHash
	event.Detail["expansion_key"] = in.ExpansionKey
	event.Detail["gate"] = in.Gate
	event.Detail["outcome"] = in.Outcome
	event.Detail["reason"] = in.Reason
	if e = m.seed.audit(ctx, tx, event); e != nil {
		return out, e
	}
	_, e = m.publisher.Publish(ctx, tx, events.Event{TenantID: in.TenantID, ProjectID: in.ProjectID, Type: "governance.workflow_decided", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: authority.source}, Subject: events.Subject{Kind: "workflow_decision", ID: r.ID}, SubjectVersion: 1, Data: map[string]any{"binding_id": in.BindingID.String(), "definition_hash": in.DefinitionHash, "expansion_key": in.ExpansionKey, "gate": in.Gate, "outcome": in.Outcome}})
	return workflowDecisionFact(r), e
}
