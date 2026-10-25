package clientwork

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type IntakeSuggestionBody struct {
	SuggestedProjectID *guid.ID           `json:"suggested_project_id,omitempty"`
	IntakeID           guid.ID            `json:"intake_id"`
	IntakeVersion      int                `json:"intake_version" minimum:"1"`
	Assessment         intake.TriageDraft `json:"assessment"`
	Recommendation     string             `json:"recommendation" enum:"NEW_REQUIREMENT,NEW_PROJECT,REWORK,NEEDS_CLARIFICATION"`
	Reason             string             `json:"reason" minLength:"1" maxLength:"2000"`
}
type IntakeSuggestionInput CreateInput[IntakeSuggestionBody]
type IntakeSuggestionView struct {
	ID guid.ID `json:"id"`
	IntakeSuggestionBody
	ActorKind string    `json:"actor_kind" enum:"HUMAN,AGENT"`
	ActorID   guid.ID   `json:"actor_id"`
	CreatedAt time.Time `json:"created_at"`
	State     string    `json:"state" enum:"SUGGESTED"`
}

const intakeSuggestionPath = entityBase + "/intake-suggestions"

func (m *Module) registerIntakeSuggestionHTTP(api huma.API) error {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "clientwork.intake_suggestion.lookup", Method: "GET", Path: "/intake-suggestion-sources/{id}", Summary: "Resolve an inbox source under current Human or Agent project authority", Tags: []string{"tenants"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, m.lookupIntakeSuggestionHTTP)
	owner := authz.ResourceOwnerResolver(m.owner)
	guard := authz.Guard{Resource: "scope", Verb: "change"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake_suggestion.sources", Method: "GET", Path: entityBase + "/intake-suggestion-sources", Summary: "Discover eligible source metadata for internal Human or Agent triage", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.listIntakeSuggestionSourcesHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake_suggestion.source", Method: "GET", Path: entityBase + "/intakes/{id}/suggestion-source", Summary: "Read actual current source content for internal Human or Agent triage", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.getIntakeSuggestionSourceHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake_suggestion.create", Method: "POST", Path: intakeSuggestionPath, Summary: "Record an internal triage and conversion suggestion under actual Human or Agent identity", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.createIntakeSuggestionHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake_suggestion.get", Method: "GET", Path: intakeSuggestionPath + "/{id}", Summary: "Read an immutable internal Intake suggestion without implying execution or approval", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.getIntakeSuggestionHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake_suggestion.list", Method: "GET", Path: entityBase + "/intakes/{id}/suggestions", Summary: "Read internal suggestions for the exact project Intake", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.listIntakeSuggestionsHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	return authz.RegisterRequestContract(api, intakeSuggestionPath)
}

func (m *Module) listIntakeSuggestionSourcesHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[intake.SuggestionSource]], error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil || in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.questionDrafts.AuthorizeOn == nil || m.intakeSources.SuggestionSourcesOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	out := Page[intake.SuggestionSource]{Items: []intake.SuggestionSource{}}
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := m.questionDrafts.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), p); err != nil {
			return err
		}
		scope := cursorScope(ctx, p, "intake-suggestion-sources")
		last, err := position(in.Cursor, scope)
		if err != nil {
			return err
		}
		page, err := m.intakeSources.SuggestionSourcesOn(ctx, tx, authn.TenantIDFromContext(ctx), p, last, in.Limit)
		if err != nil {
			return err
		}
		out.Items, out.HasMore = page.Items, page.HasMore
		if page.HasMore && len(page.Items) > 0 {
			out.NextCursor = cursor(scope, page.Items[len(page.Items)-1].ID)
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) getIntakeSuggestionSourceHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[intake.SuggestionSource], error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.questionDrafts.AuthorizeOn == nil || m.questionDrafts.SourceOn == nil || m.intakeSources.SuggestionSourceOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out intake.SuggestionSource
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if _, err := m.questionDrafts.AuthorizeOn(ctx, tx, tenant, p); err != nil {
			return err
		}
		var err error
		out, err = m.intakeSources.SuggestionSourceOn(ctx, tx, tenant, p, id)
		if err != nil {
			return err
		}
		return m.questionDrafts.SourceOn(ctx, tx, tenant, p, id, out.Version)
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func suggestionView(row *model.IntakeSuggestion) (IntakeSuggestionView, error) {
	out := IntakeSuggestionView{ID: row.ID, ActorKind: row.ActorKind, ActorID: row.ActorID, CreatedAt: row.CreatedAt, State: "SUGGESTED"}
	err := json.Unmarshal([]byte(row.Proposal), &out.IntakeSuggestionBody)
	return out, err
}

func (m *Module) listIntakeSuggestionsHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[[]IntakeSuggestionView], error) {
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
	out := []IntakeSuggestionView{}
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := m.questionDrafts.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), p); err != nil {
			return err
		}
		q := query.BindIntakeSuggestion(tx, m.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.IntakeID.Eq(id)).Order(q.ID.Desc()).Limit(20).Find()
		if err != nil {
			return err
		}
		for _, row := range rows {
			view, err := suggestionView(row)
			if err != nil {
				return err
			}
			out = append(out, view)
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func validIntakeSuggestion(in IntakeSuggestionBody) bool {
	d := in.Assessment
	if in.IntakeID <= 0 || in.IntakeVersion < 1 || !validText(in.Reason, 2000) || !slices.Contains([]string{"NEW_REQUIREMENT", "NEW_PROJECT", "REWORK", "NEEDS_CLARIFICATION"}, in.Recommendation) || !validText(d.Summary, 2000) || !validText(d.RequirementDraft, 8000) || !slices.Contains([]string{"XS", "S", "M", "L", "XL", "UNKNOWN"}, d.Size) || len(d.Criteria) < 1 || len(d.Criteria) > 100 || len(d.Risks) > 50 || len(d.Questions) > 50 {
		return false
	}
	for _, list := range [][]string{d.Criteria, d.Risks, d.Questions} {
		for _, text := range list {
			if !validText(text, 2000) {
				return false
			}
		}
	}
	return true
}

func (m *Module) createIntakeSuggestionHTTP(ctx context.Context, in *IntakeSuggestionInput) (*CommandOutput, error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	check := func(tx *gorm.DB) error {
		if in.Body.SuggestedProjectID == nil {
			return nil
		}
		if *in.Body.SuggestedProjectID <= 0 {
			return ErrInvalid
		}
		if m.questionDrafts.AuthorizeOn == nil {
			return ErrUnavailable
		}
		kind, err := m.questionDrafts.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), *in.Body.SuggestedProjectID)
		if err != nil {
			return err
		}
		if kind != "HUMAN" && kind != "AGENT" {
			return ErrForbidden
		}
		return nil
	}
	return m.command(ctx, p, "clientwork.intake_suggestion.create", "POST", intakeSuggestionPath, in.Key, 201, in, "scope_change", false, check, func(tx *gorm.DB) (Receipt, error) {
		if !validIntakeSuggestion(in.Body) {
			return Receipt{}, ErrInvalid
		}
		if m.questionDrafts.SourceOn == nil || m.audit == nil || m.events == nil {
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
		if err = m.questionDrafts.SourceOn(ctx, tx, tenant, p, in.Body.IntakeID, in.Body.IntakeVersion); err != nil {
			return Receipt{}, err
		}
		q := query.BindIntakeSuggestion(tx, m.names)
		n, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(p), q.IntakeID.Eq(in.Body.IntakeID)).Count()
		if err != nil {
			return Receipt{}, err
		}
		if n >= 20 {
			return Receipt{}, ErrConflict
		}
		raw, err := json.Marshal(in.Body)
		if err != nil {
			return Receipt{}, err
		}
		if len(raw) > 65535 {
			return Receipt{}, ErrInvalid
		}
		id, err := m.nextID()
		if err != nil {
			return Receipt{}, err
		}
		row := &model.IntakeSuggestion{ID: id, TenantID: tenant, ProjectID: p, IntakeID: in.Body.IntakeID, IntakeVersion: in.Body.IntakeVersion, Proposal: string(raw), ActorKind: kind, ActorID: actor, CreatedAt: now()}
		if err = q.WithContext(ctx).Create(row); err != nil {
			return Receipt{}, err
		}
		e := audit.NewDomainEvent(ctx, tenant, "clientwork.intake_suggested", Name, id)
		e.Detail["authority_source"] = "permission:scope_change"
		e.Detail["actor_kind"] = kind
		e.Detail["intake_id"] = in.Body.IntakeID.String()
		e.Detail["intake_version"] = in.Body.IntakeVersion
		if err = m.audit(ctx, tx, e); err != nil {
			return Receipt{}, err
		}
		_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: p, Type: "clientwork.intake_suggested", Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: "permission:scope_change"}, Subject: events.Subject{Kind: Name, ID: id}, SubjectVersion: 1, Data: map[string]any{"intake_id": in.Body.IntakeID.String(), "intake_version": in.Body.IntakeVersion, "recommendation": in.Body.Recommendation, "state": "SUGGESTED"}})
		return Receipt{ID: id, Version: 1, State: "SUGGESTED"}, err
	})
}

func (m *Module) getIntakeSuggestionHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[IntakeSuggestionView], error) {
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
	var out IntakeSuggestionView
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := m.questionDrafts.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), p); err != nil {
			return err
		}
		q := query.BindIntakeSuggestion(tx, m.names)
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id)).Take()
		if err != nil {
			return err
		}
		out, err = suggestionView(row)
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) injectIntakeOperations(s *module.Services) error {
	catalog, err := module.Resolve[agents.MountedOperationCatalog](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if catalog.Register == nil {
		return ErrUnavailable
	}
	return catalog.Register(
		agents.MountedOperation{Name: "intake_suggestions.lookup", HTTPID: "clientwork.intake_suggestion.lookup", Permission: "scope_change"},
		agents.MountedOperation{Name: "intake_suggestions.sources", HTTPID: "clientwork.intake_suggestion.sources", Permission: "scope_change"},
		agents.MountedOperation{Name: "intake_suggestions.source", HTTPID: "clientwork.intake_suggestion.source", Permission: "scope_change"},
		agents.MountedOperation{Name: "intake_suggestions.create", HTTPID: "clientwork.intake_suggestion.create", Permission: "scope_change"},
		agents.MountedOperation{Name: "intake_suggestions.get", HTTPID: "clientwork.intake_suggestion.get", Permission: "scope_change"},
		agents.MountedOperation{Name: "intake_suggestions.list", HTTPID: "clientwork.intake_suggestion.list", Permission: "scope_change"},
	)
}

type IntakeSuggestionLookup struct {
	TenantID string `path:"tenant_id"`
	ID       string `path:"id"`
}

func (m *Module) lookupIntakeSuggestionHTTP(ctx context.Context, in *IntakeSuggestionLookup) (*respx.Body[intake.SuggestionSource], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil || tenant != authn.TenantIDFromContext(ctx) {
		return nil, httpError(ctx, ErrNotFound)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.intakeSources.SuggestionProjectOn == nil || m.intakeSources.SuggestionSourceOn == nil || m.questionDrafts.AuthorizeOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out intake.SuggestionSource
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		project, err := m.intakeSources.SuggestionProjectOn(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if _, err = m.questionDrafts.AuthorizeOn(ctx, tx, tenant, project); err != nil {
			return err
		}
		out, err = m.intakeSources.SuggestionSourceOn(ctx, tx, tenant, project, id)
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
