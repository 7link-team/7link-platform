package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"database/sql/driver"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

const reviewPlansPath = "/entities/{entity_id}/review-plans"

type reviewPlansListInput struct {
	EntityID   string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID string `query:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset     int    `query:"offset" default:"0" minimum:"0"`
	Limit      int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}
type reviewPlanProposeInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body     struct {
		WorkItemID      guid.ID           `json:"work_item_id"`
		WorkItemVersion int               `json:"work_item_version" minimum:"1"`
		Content         ReviewPlanContent `json:"content"`
	}
}
type reviewPlanDecisionInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID       string `path:"plan_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body     struct {
		Version     int    `json:"version" minimum:"1"`
		ContentHash string `json:"content_hash" minLength:"64" maxLength:"64"`
		Action      string `json:"action" enum:"APPROVE,REJECT"`
		Feedback    string `json:"feedback,omitempty" maxLength:"2000"`
	}
}

func (m *Module) registerReviewPlanHTTP(api huma.API) {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "delivery.review-plans.list", Method: http.MethodGet, Path: reviewPlansPath, Summary: "List immutable human plan proposals and decisions", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.listReviewPlansHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "delivery.review-plan.propose", Method: http.MethodPost, Path: reviewPlansPath, Summary: "Record a human-authored readonly plan proposal", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "write"}, m.proposeReviewPlanHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "delivery.review-plan.decide", Method: http.MethodPost, Path: reviewPlansPath + "/{plan_id}/decisions", Summary: "Independently approve or reject an immutable plan", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "plan", Verb: "approve"}, m.decideReviewPlanHTTP)
}
func (m *Module) listReviewPlansHTTP(ctx context.Context, in *reviewPlansListInput) (*respx.Body[[]ReviewPlanView], error) {
	if in.Limit < 1 || in.Limit > 200 || in.Offset < 0 {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, resultHTTPError(ctx, ErrUnavailable)
	}
	var work guid.ID
	if in.WorkItemID != "" {
		work, e = guid.RequireID(in.WorkItemID)
		if e != nil {
			return nil, resultHTTPError(ctx, ErrInvalidArgument)
		}
	}
	items := []ReviewPlanView{}
	var total int64
	e = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		c, e := m.workPolicy(ctx, tx, authn.TenantIDFromContext(ctx), project, "work_item_read", authn.PrincipalIDFromContext(ctx))
		if e != nil {
			return e
		}
		// Project lists cannot widen an author-limited grant to other people's work.
		w := query.BindWorkItems(tx, m.service.names)
		scope, e := workScope(c, project, w.CreatedBy)
		if e != nil {
			return e
		}
		workQuery := w.WithContext(ctx).Where(w.TenantID.Eq(authn.TenantIDFromContext(ctx)), w.ProjectID.Eq(project), w.ArchivedAt.IsNull())
		if scope != nil {
			workQuery = workQuery.Where(scope)
		}
		if work > 0 {
			workQuery = workQuery.Where(w.ID.Eq(work))
		}
		works, e := workQuery.Find()
		if e != nil {
			return e
		}
		ids := []driver.Valuer{}
		for _, r := range works {
			ids = append(ids, r.ID)
		}
		if len(ids) == 0 {
			return nil
		}
		q := query.BindReviewPlans(tx, m.service.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.WorkItemID.In(ids...))
		total, e = stmt.Count()
		if e != nil {
			return e
		}
		rows, e := stmt.Order(q.ID.Desc()).Offset(in.Offset).Limit(in.Limit).Find()
		if e != nil {
			return e
		}
		for _, r := range rows {
			v, e := reviewPlanView(r)
			if e != nil {
				return e
			}
			items = append(items, v)
		}
		return nil
	})
	if e != nil {
		return nil, resultHTTPError(ctx, e)
	}
	return respx.List(ctx, items, respx.Page{Total: total, Offset: in.Offset, Limit: in.Limit}), nil
}
func (m *Module) proposeReviewPlanHTTP(ctx context.Context, in *reviewPlanProposeInput) (*deliveryCommandOutput[ReviewPlanView], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, resultHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	owner, e := m.preparationOwner(ctx, m.runtime.Database().Write(), tenant, project, in.Body.WorkItemID)
	if e != nil {
		return nil, resultHTTPError(ctx, e)
	}
	return deliveryCommand(ctx, m, "plan", resultHTTPError, "delivery.review-plan.propose", http.MethodPost, reviewPlansPath, in.Key, 201, in.Body, map[string]string{"entity_id": project.String()}, func(tx *gorm.DB) (ReviewPlanView, guid.ID, error) {
		v, e := m.proposeReviewPlanOn(ctx, tx, tenant, project, in.Body.WorkItemID, actor, owner, in.Body.WorkItemVersion, in.Body.Content)
		return v, v.ID, e
	}, func(tx *gorm.DB) error {
		_, _, _, e := m.lockedWorkOn(ctx, tx, tenant, project, in.Body.WorkItemID, actor, owner, "work_item_write")
		return e
	})
}
func (m *Module) decideReviewPlanHTTP(ctx context.Context, in *reviewPlanDecisionInput) (*deliveryCommandOutput[ReviewPlanView], error) {
	project, e := guid.RequireID(in.EntityID)
	id, e2 := guid.RequireID(in.ID)
	if e != nil || e2 != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, resultHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	db := m.runtime.Database().Write()
	q := query.BindReviewPlans(db, m.service.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if e != nil {
		return nil, resultHTTPError(ctx, ErrProjectNotFound)
	}
	owner, e := m.preparationOwner(ctx, db, tenant, project, r.WorkItemID)
	if e != nil {
		return nil, resultHTTPError(ctx, e)
	}
	return deliveryCommand(ctx, m, "plan", resultHTTPError, "delivery.review-plan.decide", http.MethodPost, reviewPlansPath+"/{plan_id}/decisions", in.Key, 200, in.Body, map[string]string{"entity_id": project.String(), "plan_id": id.String()}, func(tx *gorm.DB) (ReviewPlanView, guid.ID, error) {
		v, e := m.decideReviewPlanOn(ctx, tx, tenant, project, r.WorkItemID, id, actor, owner, in.Body.Version, in.Body.ContentHash, in.Body.Action, in.Body.Feedback)
		return v, v.ID, e
	}, func(tx *gorm.DB) error {
		if e := m.prelockPlanningDecisionOn(ctx, tx, tenant, project, r.WorkItemID, id, actor, owner); e != nil {
			return e
		}
		_, work, _, e := m.lockedWorkActorOn(ctx, tx, tenant, project, r.WorkItemID, actor, owner, "plan_approve", true)
		if e != nil {
			return e
		}
		_, e = m.planReviewerOn(ctx, tx, work, r, actor)
		return e
	})
}
