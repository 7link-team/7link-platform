package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const projectModesPath = "/entities/{entity_id}/project/mode-changes"
const projectTakeoversPath = "/entities/{entity_id}/project/takeovers"
const workTakeoversPath = workPath + "/{work_item_id}/takeovers"

type projectModeInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch  string `header:"If-Match" required:"true"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Cookie   string `header:"Cookie" hidden:"true"`
	Body     projectModeBody
}
type projectTakeoverInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch  string `header:"If-Match" required:"true"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Cookie   string `header:"Cookie" hidden:"true"`
	Body     struct {
		Reason      string `json:"reason" minLength:"10" maxLength:"2000"`
		ReauthProof string `json:"reauth_proof" minLength:"1" maxLength:"256"`
	}
}
type workTakeoverInput struct {
	EntityID   string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key        string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Cookie     string `header:"Cookie" hidden:"true"`
	Body       workTakeoverBody
}

func modeHTTPError(ctx context.Context, err error) error {
	if errors.Is(err, governance.ErrForbidden) {
		err = ErrWorkForbidden
	}
	if errors.Is(err, governance.ErrUnavailable) {
		err = ErrUnavailable
	}
	if errors.Is(err, governance.ErrVersion) {
		err = ErrWorkVersion
	}
	var f stageFailure
	if errors.Is(err, ErrModeSame) {
		return respx.ErrStatus(ctx, 409, 127020, deliveryi18n.ModeSame, nil)
	}
	if errors.Is(err, ErrModeIllegal) {
		return respx.ErrStatus(ctx, 409, 127051, deliveryi18n.ModeIllegal, nil)
	}
	if errors.Is(err, ErrModePreconditions) {
		code := 127021
		var meta map[string]any
		if errors.As(err, &f) {
			meta = f.meta
			if criteria, ok := f.meta["preconditions"].([]stageCriterion); ok {
				for i, c := range criteria {
					if !c.Passed {
						code += i
						break
					}
				}
			}
		}
		return respx.ErrStatus(ctx, 422, code, deliveryi18n.ModePreconditions, meta)
	}
	return resultHTTPError(ctx, err)
}

func (m *Module) registerProjectModeHTTP(api huma.API) {
	iamauthn.RegisterModesReauthentication(api)
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	errors := []int{400, 401, 403, 404, 409, 422, 500, 503}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.mode-change", Method: http.MethodPost, Path: projectModesPath, Summary: "Change the project team mode with current prerequisites", Tags: []string{"entities"}, Errors: errors}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "mode_change"}, m.changeProjectModeHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.mode-preconditions", Method: http.MethodGet, Path: "/entities/{entity_id}/project/mode-preconditions", Summary: "Inspect current autonomous project prerequisites", Tags: []string{"entities"}, Errors: errors}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "read"}, m.projectModePreconditionsHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.mode-history", Method: http.MethodGet, Path: projectModesPath, Summary: "List immutable project mode changes", Tags: []string{"entities"}, Errors: errors}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "read"}, m.projectModeHistoryHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.takeover", Method: http.MethodPost, Path: projectTakeoversPath, Summary: "Take over project control and revoke delegated authority atomically", Tags: []string{"entities"}, Errors: errors}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "takeover"}, m.takeoverProjectHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.takeover", Method: http.MethodPost, Path: workTakeoversPath, Summary: "Require Human decisions for a work item while preserving its running execution", Tags: []string{"entities"}, Errors: errors}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "takeover"}, m.takeoverWorkHTTP)
}

func (m *Module) changeProjectModeHTTP(ctx context.Context, in *projectModeInput) (*deliveryCommandOutput[ProjectModeReceipt], error) {
	return m.projectModeCommandHTTP(ctx, in.EntityID, in.IfMatch, in.Key, in.Cookie, in.Body, false)
}
func (m *Module) takeoverProjectHTTP(ctx context.Context, in *projectTakeoverInput) (*deliveryCommandOutput[ProjectModeReceipt], error) {
	return m.projectModeCommandHTTP(ctx, in.EntityID, in.IfMatch, in.Key, in.Cookie, projectModeBody{ToMode: "HYBRID", Reason: in.Body.Reason, ReauthProof: in.Body.ReauthProof}, true)
}
func (m *Module) projectModeCommandHTTP(ctx context.Context, raw, match, key, cookie string, body projectModeBody, takeover bool) (*deliveryCommandOutput[ProjectModeReceipt], error) {
	project, _, version, err := deliveryEditIDs(raw, raw, match)
	if err != nil {
		return nil, modeHTTPError(ctx, err)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, modeHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	locked, err := m.modePrincipalCandidates(ctx, m.runtime.Database().Write(), tenant, project)
	if err != nil {
		return nil, modeHTTPError(ctx, err)
	}
	op, path := "project.mode-change", projectModesPath
	if takeover {
		op, path = "project.takeover", projectTakeoversPath
	}
	request := struct {
		Version int             `json:"version"`
		Body    projectModeBody `json:"body"`
	}{version, body}
	return deliveryCommand(ctx, m, "project_mode_change", modeHTTPError, op, http.MethodPost, path, key, 200, request, map[string]string{"entity_id": project.String()}, func(tx *gorm.DB) (ProjectModeReceipt, guid.ID, error) {
		out, err := m.changeProjectModeOn(ctx, tx, tenant, project, actor, version, body, cookie, takeover, locked)
		return out, out.ModeChange.ID, err
	}, func(tx *gorm.DB) error {
		_, _, err := m.projectModeAuthorizeOn(ctx, tx, tenant, project, actor, takeover, locked)
		return err
	})
}

func (m *Module) projectModePreconditionsHTTP(ctx context.Context, in *stageReadinessInput) (*respx.Body[[]stageCriterion], error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, modeHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, modeHTTPError(ctx, ErrUnavailable)
	}
	db := m.runtime.Database().Write()
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	locked, err := m.modePrincipalCandidates(ctx, db, tenant, project)
	if err != nil {
		return nil, modeHTTPError(ctx, err)
	}
	var criteria []stageCriterion
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.lockStagePrincipalsOn(ctx, tx, actor, locked); err != nil {
			return err
		}
		p, _, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "project_read")
		if err != nil {
			return err
		}
		criteria, err = m.modePreconditionsOn(ctx, tx, p, locked)
		return err
	})
	if err != nil {
		return nil, modeHTTPError(ctx, err)
	}
	return respx.OK(ctx, criteria), nil
}

func (m *Module) projectModeHistoryHTTP(ctx context.Context, in *stageListInput) (*respx.Body[[]projectModeChangeView], error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil || in.Offset < 0 || in.Limit < 1 || in.Limit > 200 {
		return nil, modeHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, modeHTTPError(ctx, ErrUnavailable)
	}
	rows := []projectModeChangeView{}
	var total int64
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if err := m.constitutionReadProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx)); err != nil {
			return err
		}
		q := query.BindModeChanges(tx, m.service.names)
		base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project))
		var err error
		total, err = base.Count()
		if err != nil {
			return err
		}
		records, err := base.Order(q.OccurredAt.Desc(), q.ID.Desc()).Offset(in.Offset).Limit(in.Limit).Find()
		if err != nil {
			return err
		}
		for _, r := range records {
			v := projectModeChangeView{ProjectModeChange: *r}
			if json.Unmarshal([]byte(r.PreconditionsSnapshot), &v.Preconditions) != nil || json.Unmarshal([]byte(r.RevokedDelegationIDs), &v.RevokedIDs) != nil || json.Unmarshal([]byte(r.RevokedSpawnGrantIDs), &v.RevokedSpawnIDs) != nil {
				return ErrUnavailable
			}
			rows = append(rows, v)
		}
		return nil
	})
	if err != nil {
		return nil, modeHTTPError(ctx, err)
	}
	return respx.List(ctx, rows, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(rows), Total: total}), nil
}

func (m *Module) takeoverWorkHTTP(ctx context.Context, in *workTakeoverInput) (*deliveryCommandOutput[WorkView], error) {
	project, e := guid.RequireID(in.EntityID)
	id, e2 := guid.RequireID(in.WorkItemID)
	if e != nil || e2 != nil {
		return nil, modeHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, modeHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	owner, err := m.preparationOwner(ctx, m.runtime.Database().Write(), tenant, project, id)
	if err != nil {
		return nil, modeHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "work_item", modeHTTPError, "work_item.takeover", http.MethodPost, workTakeoversPath, in.Key, 200, in.Body, map[string]string{"entity_id": project.String(), "work_item_id": id.String()}, func(tx *gorm.DB) (WorkView, guid.ID, error) {
		v, err := m.takeoverWorkOn(ctx, tx, tenant, project, id, actor, owner, in.Body, in.Cookie)
		return v, id, err
	}, func(tx *gorm.DB) error {
		if _, _, _, err := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "project_takeover"); err != nil {
			return err
		}
		if m.modeGovernance.AuthorityOn == nil {
			return ErrUnavailable
		}
		_, err := m.modeGovernance.AuthorityOn(ctx, tx, tenant, project, "project_takeover")
		return err
	})
}
