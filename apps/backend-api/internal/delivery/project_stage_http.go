package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const stageTransitionsPath = "/entities/{entity_id}/project/stage-transitions"

type stageChangeInput struct {
	Cookie         string `header:"Cookie"`
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted project concurrency version."`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           stageChangeBody
}
type stageListInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset   int    `query:"offset" default:"0" minimum:"0"`
	Limit    int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}
type stageReadinessInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func stageHTTPError(ctx context.Context, err error) error {
	var failure stageFailure
	var meta map[string]any
	if errors.As(err, &failure) {
		meta = failure.meta
	}
	switch {
	case errors.Is(err, ErrStageIllegal):
		return respx.ErrStatus(ctx, 409, 127010, deliveryi18n.StageIllegal, meta)
	case errors.Is(err, ErrStageDelivered):
		return respx.ErrStatus(ctx, 422, 127013, deliveryi18n.StageDelivered, meta)
	case errors.Is(err, ErrStageClose):
		return respx.ErrStatus(ctx, 422, 127014, deliveryi18n.StageClose, meta)
	case errors.Is(err, ErrStageCriteria):
		return respx.ErrStatus(ctx, 422, 127011, deliveryi18n.StageCriteria, meta)
	case errors.Is(err, ErrStageReason):
		return respx.ErrStatus(ctx, 422, 127012, deliveryi18n.StageReason, nil)
	default:
		return workHTTPError(ctx, err)
	}
}

func (m *Module) registerProjectStageHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.stage-transition", Method: http.MethodPost, Path: stageTransitionsPath, Summary: "Change a project stage with current guards", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "stage_change"}, m.changeProjectStageHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.stage-history", Method: http.MethodGet, Path: stageTransitionsPath, Summary: "List immutable project stage history", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "read"}, m.listProjectStagesHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.stage-readiness", Method: http.MethodGet, Path: "/entities/{entity_id}/project/stage-readiness", Summary: "Evaluate real project stage prerequisites", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "read"}, m.projectStageReadinessHTTP)
	return authz.RegisterRequestContract(api, stageTransitionsPath)
}

func (m *Module) changeProjectStageHTTP(ctx context.Context, in *stageChangeInput) (*deliveryCommandOutput[stageReceipt], error) {
	entity, _, version, err := deliveryEditIDs(in.EntityID, in.EntityID, in.IfMatch)
	if err != nil {
		return nil, stageHTTPError(ctx, err)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, stageHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	var candidates []guid.ID
	if in.Body.ToStage == "CLOSED" || in.Body.ToStage == "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		candidates, err = m.stageTerminalCandidates(ctx, m.runtime.Database().Write(), tenant, entity)
		if err != nil {
			return nil, stageHTTPError(ctx, err)
		}
	} else if in.Body.ToStage == "ACTIVE" {
		candidates, err = m.stageExecutorCandidates(ctx, m.runtime.Database().Write(), tenant, entity)
		if err != nil {
			return nil, stageHTTPError(ctx, err)
		}
	}
	hashBody := in.Body
	hashBody.ReauthProof = ""
	request := struct {
		Version int             `json:"version"`
		Body    stageChangeBody `json:"body"`
	}{version, hashBody}
	return deliveryCommand(ctx, m, "project_stage_transition", stageHTTPError, "project.stage-transition", http.MethodPost, stageTransitionsPath, in.IdempotencyKey, 201, request, map[string]string{"entity_id": entity.String()}, func(tx *gorm.DB) (stageReceipt, guid.ID, error) {
		body := in.Body
		body.cookie = in.Cookie
		v, e := m.changeProjectStageOn(ctx, tx, tenant, entity, actor, version, body, candidates)
		return v, v.Transition.ID, e
	}, func(tx *gorm.DB) error {
		return m.stageReplayAuthorityOn(ctx, tx, tenant, entity, actor, in.Body.ToStage, candidates)
	})
}

func (m *Module) listProjectStagesHTTP(ctx context.Context, in *stageListInput) (*respx.Body[[]stageTransitionView], error) {
	entity, err := guid.RequireID(in.EntityID)
	if err != nil || in.Offset < 0 || in.Limit < 1 || in.Limit > 200 {
		return nil, stageHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, stageHTTPError(ctx, ErrUnavailable)
	}
	rows := []stageTransitionView{}
	var total int64
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if e := m.constitutionReadProjectOn(ctx, tx, tenant, entity, authn.PrincipalIDFromContext(ctx)); e != nil {
			return e
		}
		q := query.BindStageTransitions(tx, m.service.names)
		base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(entity))
		var e error
		total, e = base.Count()
		if e != nil {
			return e
		}
		found, e := base.Order(q.OccurredAt.Desc(), q.ID.Desc()).Offset(in.Offset).Limit(in.Limit).Find()
		if e != nil {
			return e
		}
		for _, row := range found {
			view := stageTransitionView{ProjectStageTransition: *row}
			if e = json.Unmarshal([]byte(row.CriteriaSnapshot), &view.Criteria); e != nil {
				return e
			}
			rows = append(rows, view)
		}
		return nil
	})
	if err != nil {
		return nil, stageHTTPError(ctx, err)
	}
	return respx.List(ctx, rows, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(rows), Total: total}), nil
}

func (m *Module) projectStageReadinessHTTP(ctx context.Context, in *stageReadinessInput) (*respx.Body[[]stageReadiness], error) {
	entity, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, stageHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, stageHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	db := m.runtime.Database().Write()
	candidates, err := m.stageExecutorCandidates(ctx, db, tenant, entity)
	if err != nil {
		return nil, stageHTTPError(ctx, err)
	}
	result := []stageReadiness{}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.lockStagePrincipalsOn(ctx, tx, actor, candidates); e != nil {
			return e
		}
		project, _, e := m.constitutionProjectOn(ctx, tx, tenant, entity, actor, "project_read")
		if e != nil {
			return e
		}
		eligible := false
		if project.Stage == "PLANNING" {
			eligible, e = m.stageExecutorOn(ctx, tx, tenant, entity, candidates, project.TeamMode == "HUMAN_ONLY")
			if e != nil {
				return e
			}
		}
		for _, target := range stageTargets(project.Stage) {
			criteria, e := m.stageCriteriaOn(ctx, tx, project, target, eligible)
			if e != nil {
				return e
			}
			view := stageReadiness{Target: target, Allowed: true, HumanOnly: target == "DELIVERED" || target == "CLOSED" || target == "CANCELLED", Criteria: criteria} //nolint:misspell // Preserve the published domain or serialized contract spelling.
			for _, criterion := range criteria {
				view.Allowed = view.Allowed && criterion.Passed
			}
			result = append(result, view)
		}
		return nil
	})
	if err != nil {
		return nil, stageHTTPError(ctx, err)
	}
	return respx.OK(ctx, result), nil
}
