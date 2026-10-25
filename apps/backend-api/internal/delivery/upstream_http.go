package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

const consumptionPath = workPath + "/{work_item_id}/artifact-consumptions"

type consumptionInput struct {
	ResultWorkInput
	Key  string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body consumptionBody
}

func (m *Module) registerUpstreamHTTP(api huma.API) {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.artifact-consumption-list", Method: http.MethodGet, Path: consumptionPath, Summary: "Read immutable upstream artifact consumption and applicability", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.listConsumptionsHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.artifact-consumption-create", Method: http.MethodPost, Path: consumptionPath, Summary: "Consume exact accepted upstream artifact", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "write"}, m.consumeArtifactHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.artifact-reconcile", Method: http.MethodPost, Path: consumptionPath + "/reconcile", Summary: "Reconcile upstream accepted evidence hashes", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "write"}, m.reconcileArtifactsHTTP)
}
func (m *Module) consumptionContext(ctx context.Context, in ResultWorkInput) (guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, error) {
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	project, e := guid.RequireID(in.EntityID)
	id, e2 := guid.RequireID(in.WorkItemID)
	if e != nil || e2 != nil {
		return 0, 0, 0, 0, 0, ErrInvalidArgument
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return 0, 0, 0, 0, 0, ErrUnavailable
	}
	owner, e := m.preparationOwner(ctx, m.runtime.Database().Write(), tenant, project, id)
	return tenant, project, id, actor, owner, e
}
func (m *Module) listConsumptionsHTTP(ctx context.Context, in *ResultWorkInput) (*respx.Body[[]model.ArtifactConsumption], error) {
	tenant, project, id, actor, _, err := m.consumptionContext(ctx, *in)
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	rows := []model.ArtifactConsumption{}
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.readWork(ctx, tx, tenant, project, id, actor); e != nil {
			return e
		}
		// Only snapshots whose source remains readable are disclosed.
		var all []model.ArtifactConsumption
		if e := query.BindArtifactConsumptions(tx, m.service.names).WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND work_item_id = ?", tenant, project, id).Order("id").Find(&all).Error; e != nil {
			return e
		}
		for _, row := range all {
			if _, e := m.readWork(ctx, tx, tenant, project, row.UpstreamWorkItemID, actor); e == nil {
				rows = append(rows, row)
			} else {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	return respx.OK(ctx, rows), nil
}
func (m *Module) consumeArtifactHTTP(ctx context.Context, in *consumptionInput) (*deliveryCommandOutput[model.ArtifactConsumption], error) {
	tenant, project, id, actor, owner, err := m.consumptionContext(ctx, in.ResultWorkInput)
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "work_item", resultHTTPError, "work_item.artifact-consumption-create", http.MethodPost, consumptionPath, in.Key, 201, in.Body, map[string]string{"entity_id": project.String(), "work_item_id": id.String()}, func(tx *gorm.DB) (model.ArtifactConsumption, guid.ID, error) {
		row, e := m.consumeArtifactOn(ctx, tx, tenant, project, id, actor, owner, in.Body)
		return row, id, e
	}, func(tx *gorm.DB) error {
		_, _, _, e := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "work_item_write")
		return e
	})
}
func (m *Module) reconcileArtifactsHTTP(ctx context.Context, in *ResultWorkInput) (*respx.Body[[]model.ArtifactConsumption], error) {
	tenant, project, id, actor, owner, err := m.consumptionContext(ctx, *in)
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, _, _, e := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "work_item_write"); e != nil {
			return e
		}
		var rows []model.ArtifactConsumption
		if e := query.BindArtifactConsumptions(tx, m.service.names).WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND work_item_id = ?", tenant, project, id).Order("id").Find(&rows).Error; e != nil {
			return e
		}
		for _, row := range rows {
			if e := m.reconcileConsumptionOn(ctx, tx, &row, actor, "HUMAN"); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	return m.listConsumptionsHTTP(ctx, in)
}
