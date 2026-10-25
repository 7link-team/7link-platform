package fleet

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

type modelCatalogInput struct {
	MachineInput
	Runtime string `path:"runtime_kind" enum:"CODEX,CLAUDE_CODE"`
}
type manualModelsInput struct {
	MachineInput
	Runtime string `path:"runtime_kind" enum:"CODEX,CLAUDE_CODE"`
	Body    ManualModelsUpdate
}

func (m *Module) registerModelCatalogHTTP(api huma.API) error {
	m.registerMachineHistoryHTTP(api)
	if err := m.registerModelPricesHTTP(api); err != nil {
		return err
	}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.model-catalog.get", Method: http.MethodGet, Path: "/machines/{machine_id}/model-catalogs/{runtime_kind}", Summary: "Read observed and manually configured models", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 422, 503}}, "machine_id", m.machineOwner, authz.Guard{Resource: "machine", Verb: "read", FilterData: true}, m.modelCatalogHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.manual-models.replace", Method: http.MethodPut, Path: "/machines/{machine_id}/model-catalogs/{runtime_kind}/manual-models", Summary: "Replace manual models with current machine-manager authority", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, "machine_id", m.machineOwner, authz.Guard{Resource: "machine", Verb: "manage", FilterData: true}, m.manualModelsHTTP)
	for _, path := range []string{"/machines/{machine_id}/model-catalogs/{runtime_kind}", "/machines/{machine_id}/model-catalogs/{runtime_kind}/manual-models"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) modelCatalogHTTP(ctx context.Context, in *modelCatalogInput) (*respx.Body[ModelCatalog], error) {
	machine, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out ModelCatalog
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.ModelCatalogOn(ctx, tx, authn.TenantIDFromContext(ctx), machine, in.Runtime)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) manualModelsHTTP(ctx context.Context, in *manualModelsInput) (*respx.Body[ModelCatalog], error) {
	machine, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out ModelCatalog
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.SetManualModelsOn(ctx, tx, authn.TenantIDFromContext(ctx), machine, in.Runtime, in.Body)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
