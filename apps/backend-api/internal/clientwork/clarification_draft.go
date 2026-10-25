package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ClarificationDraftBody struct {
	IntakeID      guid.ID  `json:"intake_id"`
	IntakeVersion int      `json:"intake_version" minimum:"1"`
	Questions     []string `json:"questions" minItems:"1" maxItems:"10"`
}
type ClarificationDraftInput CreateInput[ClarificationDraftBody]
type ClarificationDraftView struct {
	ID guid.ID `json:"id"`
	ClarificationDraftBody
	ActorKind   string   `json:"actor_kind"`
	ActorID     guid.ID  `json:"actor_id"`
	Version     int      `json:"version"`
	SentRoundID *guid.ID `json:"sent_round_id,omitempty"`
}
type ClarificationDraftSend struct {
	RecipientID guid.ID `json:"recipient_id"`
}

const clarificationDraftPath = clarificationPath + "/drafts"

func (m *Module) registerClarificationDraftHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.owner)
	guard := authz.Guard{Resource: "scope", Verb: "change"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.clarification_draft.create", Method: "POST", Path: clarificationDraftPath, Summary: "Draft internal Intake questions as an authorized Human or Agent", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.createClarificationDraftHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.clarification_draft.get", Method: "GET", Path: clarificationDraftPath + "/{id}", Summary: "Read an internal clarification question draft", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.getClarificationDraftHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.clarification_draft.send", Method: "POST", Path: clarificationDraftPath + "/{id}/send", Summary: "Send an exact internal question draft to a client as an authorized Human", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.sendClarificationDraftHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	if err := authz.RegisterRequestContract(api, clarificationDraftPath); err != nil {
		return err
	}
	return authz.RegisterRequestContract(api, clarificationDraftPath+"/{id}/send")
}
func (m *Module) createClarificationDraftHTTP(ctx context.Context, in *ClarificationDraftInput) (*CommandOutput, error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.clarification_draft.create", "POST", clarificationDraftPath, in.Key, 201, in, "scope_change", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.createClarificationDraftOn(ctx, tx, p, in.Body) })
}
func (m *Module) createClarificationDraftOn(ctx context.Context, tx *gorm.DB, p guid.ID, in ClarificationDraftBody) (Receipt, error) {
	if in.IntakeID <= 0 || in.IntakeVersion < 1 || len(in.Questions) < 1 || len(in.Questions) > 10 {
		return Receipt{}, ErrInvalid
	}
	for _, text := range in.Questions {
		if !validText(text, 2000) {
			return Receipt{}, ErrInvalid
		}
	}
	if m.questionDrafts.AuthorizeOn == nil || m.questionDrafts.SourceOn == nil || m.audit == nil || m.events == nil {
		return Receipt{}, ErrUnavailable
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	kind, err := m.questionDrafts.AuthorizeOn(ctx, tx, tenant, p)
	if err != nil {
		return Receipt{}, err
	}
	if kind != "HUMAN" && kind != "AGENT" {
		return Receipt{}, ErrForbidden
	}
	if err = m.questionDrafts.SourceOn(ctx, tx, tenant, p, in.IntakeID, in.IntakeVersion); err != nil {
		return Receipt{}, err
	}
	q := query.BindClarificationDraft(tx, m.names)
	count, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(p), q.IntakeID.Eq(in.IntakeID)).Count()
	if err != nil {
		return Receipt{}, err
	}
	if count >= 20 {
		return Receipt{}, ErrConflict
	}
	id, err := m.nextID()
	if err != nil {
		return Receipt{}, err
	}
	raw, err := json.Marshal(in.Questions)
	if err != nil {
		return Receipt{}, err
	}
	row := &model.ClarificationDraft{ID: id, TenantID: tenant, ProjectID: p, IntakeID: in.IntakeID, IntakeVersion: in.IntakeVersion, Questions: string(raw), ActorKind: kind, ActorID: actor, Version: 1, CreatedAt: now()}
	if err = q.WithContext(ctx).Create(row); err != nil {
		return Receipt{}, err
	}
	e := audit.NewDomainEvent(ctx, tenant, "clientwork.clarification_drafted", Name, id)
	e.Detail["authority_source"] = "permission:scope_change"
	e.Detail["actor_kind"] = kind
	e.Detail["intake_id"] = in.IntakeID.String()
	if err = m.audit(ctx, tx, e); err != nil {
		return Receipt{}, err
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: p, Type: "clientwork.clarification_drafted", Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: "permission:scope_change"}, Subject: events.Subject{Kind: Name, ID: id}, SubjectVersion: 1, Data: map[string]any{"intake_id": in.IntakeID.String()}})
	return Receipt{ID: id, Version: 1, State: "DRAFT"}, err
}
func (m *Module) loadClarificationDraftOn(ctx context.Context, tx *gorm.DB, p, id guid.ID) (*model.ClarificationDraft, error) {
	q := query.BindClarificationDraft(tx, m.names)
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id)).Take()
}
func (m *Module) getClarificationDraftHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[ClarificationDraftView], error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.questionDrafts.AuthorizeOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out ClarificationDraftView
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := m.questionDrafts.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), p); err != nil {
			return err
		}
		row, err := m.loadClarificationDraftOn(ctx, tx, p, id)
		if err != nil {
			return err
		}
		out = ClarificationDraftView{ID: row.ID, ClarificationDraftBody: ClarificationDraftBody{IntakeID: row.IntakeID, IntakeVersion: row.IntakeVersion}, ActorKind: row.ActorKind, ActorID: row.ActorID, Version: row.Version, SentRoundID: row.SentRoundID}
		return json.Unmarshal([]byte(row.Questions), &out.Questions)
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) sendClarificationDraftHTTP(ctx context.Context, in *EditInput[ClarificationDraftSend]) (*CommandOutput, error) {
	p, id, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return m.command(ctx, p, "clientwork.clarification_draft.send", "POST", clarificationDraftPath+"/"+id.String()+"/send", in.Key, 201, in, "scope_change", false, func(tx *gorm.DB) error {
		if m.questionDrafts.AuthorizeOn == nil {
			return ErrUnavailable
		}
		kind, err := m.questionDrafts.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), p)
		if err != nil {
			return err
		}
		if kind != "HUMAN" {
			return ErrForbidden
		}
		return nil
	}, func(tx *gorm.DB) (Receipt, error) {
		row, err := m.loadClarificationDraftOn(ctx, tx, p, id)
		if err != nil {
			return Receipt{}, err
		}
		if row.Version != v || row.SentRoundID != nil {
			return Receipt{}, ErrConflict
		}
		var questions []string
		if err = json.Unmarshal([]byte(row.Questions), &questions); err != nil {
			return Receipt{}, err
		}
		sent, err := m.requestClarificationOn(ctx, tx, p, ClarificationRequest{IntakeID: row.IntakeID, IntakeVersion: row.IntakeVersion, RecipientID: in.Body.RecipientID, Questions: questions})
		if err != nil {
			return Receipt{}, err
		}
		q := query.BindClarificationDraft(tx, m.names)
		changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(row.ID), q.Version.Eq(v), q.SentRoundID.IsNull()).UpdateSimple(q.Version.Add(1), q.SentRoundID.Value(sent.ID))
		if err != nil {
			return Receipt{}, err
		}
		if changed.RowsAffected != 1 {
			return Receipt{}, ErrConflict
		}
		return sent, nil
	})
}
