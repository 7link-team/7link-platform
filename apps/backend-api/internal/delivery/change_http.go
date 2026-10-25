package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

type scopeChangeInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RequirementID  string `path:"requirement_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key"`
	Cookie         string `header:"Cookie"`
	Body           scopeChangeBody
}

type changeHistoryInput struct {
	EntityID      string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RequirementID string `path:"requirement_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset        int    `query:"offset" default:"0" minimum:"0"`
	Limit         int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}
type changeImpactsInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RequirementID  string `path:"requirement_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ChangeRecordID string `path:"change_record_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset         int    `query:"offset" default:"0" minimum:"0"`
	Limit          int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

func (m *Module) registerChangeHTTP(api huma.API) error {
	if err := m.registerBaselineHTTP(api); err != nil {
		return err
	}
	if err := m.registerReworkHTTP(api); err != nil {
		return err
	}
	path := requirementPath + "/{requirement_id}/change-records"
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement.scope-change", Method: http.MethodPost, Path: path, Summary: "Create an authorized immutable scope baseline", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "scope", Verb: "change"}, m.scopeChangeHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement.change-list", Method: http.MethodGet, Path: path, Summary: "List immutable requirement scope decisions", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "requirement", Verb: "read"}, m.changeHistoryHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement.change-impact-list", Method: http.MethodGet, Path: path + "/{change_record_id}/impacts", Summary: "List readable work impacted by a scope decision", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "requirement", Verb: "read"}, m.changeImpactsHTTP)
	return authz.RegisterRequestContract(api, path)
}

func (m *Module) changeHistoryHTTP(ctx context.Context, in *changeHistoryInput) (*respx.Body[[]changeHistoryView], error) {
	project, e := guid.RequireID(in.EntityID)
	id, e2 := guid.RequireID(in.RequirementID)
	if e != nil || e2 != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.workPolicy == nil {
		return nil, requirementHTTPError(ctx, ErrUnavailable)
	}
	var items []changeHistoryView
	var total int64
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		items, total, e = m.changeHistoryOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id, authn.PrincipalIDFromContext(ctx), in.Offset, in.Limit)
		return e
	})
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
}
func (m *Module) changeImpactsHTTP(ctx context.Context, in *changeImpactsInput) (*respx.Body[[]changeImpactView], error) {
	project, e := guid.RequireID(in.EntityID)
	id, e2 := guid.RequireID(in.RequirementID)
	changeID, e3 := guid.RequireID(in.ChangeRecordID)
	if e != nil || e2 != nil || e3 != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.workPolicy == nil {
		return nil, requirementHTTPError(ctx, ErrUnavailable)
	}
	var items []changeImpactView
	var total int64
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		items, total, e = m.changeImpactsOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id, changeID, authn.PrincipalIDFromContext(ctx), in.Offset, in.Limit)
		return e
	})
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
}
func (m *Module) scopeChangeHTTP(ctx context.Context, in *scopeChangeInput) (*deliveryCommandOutput[scopeChangeView], error) {
	project, id, version, err := deliveryEditIDs(in.EntityID, in.RequirementID, in.IfMatch)
	if err != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	mapError := func(ctx context.Context, err error) error {
		if errors.Is(err, authn.ErrInvalidCredentials) || errors.Is(err, authn.ErrAdditionalVerification) || errors.Is(err, authn.ErrInvalidToken) || errors.Is(err, authn.ErrExpiredToken) {
			return respx.ErrStatus(ctx, 403, 120403, respx.Forbidden, map[string]any{"reauthentication_required": true, "purpose": "scope_change", "target": "requirement:" + id.String()})
		}
		if errors.Is(err, authn.ErrUnavailable) {
			err = ErrUnavailable
		}
		return requirementHTTPError(ctx, err)
	}
	// A proof is consumed only when a new command runs. Replays recheck the
	// current principal, requirement scope and actual governance role authority.
	request := in.Body
	request.ReauthProof = ""
	path := requirementPath + "/{requirement_id}/change-records"
	return deliveryCommand(ctx, m, "change_record", mapError, "requirement.scope-change", http.MethodPost, path, in.IdempotencyKey, 201, struct {
		Version int
		Body    scopeChangeBody
	}{version, request}, map[string]string{"entity_id": project.String(), "requirement_id": id.String()}, func(tx *gorm.DB) (scopeChangeView, guid.ID, error) {
		view, e := m.scopeChangeOn(ctx, tx, tenant, project, id, actor, version, in.Cookie, in.Body)
		return view, view.ID, e
	}, func(tx *gorm.DB) error {
		c, e := m.requirementWriterOn(ctx, tx, tenant, project, actor)
		if e != nil {
			return e
		}
		q := query.BindRequirements(tx, m.service.names)
		scope, e := workScope(c, project, q.CreatedBy)
		if e != nil {
			return e
		}
		base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.ArchivedAt.IsNull())
		if scope != nil {
			base = base.Where(scope)
		}
		if _, e = base.First(); e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return ErrProjectNotFound
			}
			return e
		}
		if m.deliveryAuthority == nil {
			return ErrUnavailable
		}
		d, e := m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: tenant, ProjectID: project, PrincipalID: actor, ActorKind: "HUMAN", Code: "scope_change", Resource: governance.ResourceRef{Kind: "requirement", ID: id}})
		if e != nil {
			return e
		}
		if !d.Allowed || d.DelegationID != 0 || !strings.HasPrefix(d.AuthoritySource, "ROLE:") {
			return ErrWorkForbidden
		}
		return nil
	})
}
