package delivery

import (
	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

const deploymentPath = workPath + "/{work_item_id}/deployment-events"
const defectReopenPath = workPath + "/{work_item_id}/defect-reopens"

type deploymentEventInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID     string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           DeploymentEvent
}
type defectReopenInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID     string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           defectReopenBody
}

func defectLifecycleError(ctx context.Context, err error) error {
	if errors.Is(err, ErrDefectDeployment) {
		return respx.ErrStatus(ctx, 409, 126006, deliveryi18n.DefectDeploymentMismatch, nil)
	}
	if errors.Is(err, ErrDefectReopen) {
		return respx.ErrStatus(ctx, 409, 126007, deliveryi18n.DefectReopenSourceNotTerminal, nil)
	}
	return resultHTTPError(ctx, err)
}
func (m *Module) registerDeploymentHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.deployment-event.create", Method: http.MethodPost, Path: deploymentPath, Summary: "Record a trusted service-account deployment event", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "write"}, m.deploymentEventHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.defect-reopen.create", Method: http.MethodPost, Path: defectReopenPath, Summary: "Reopen a terminal defect as a new BUG", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "write"}, m.defectReopenHTTP)
	if err := authz.RegisterRequestContract(api, deploymentPath); err != nil {
		return err
	}
	return authz.RegisterRequestContract(api, defectReopenPath)
}
func (m *Module) deploymentEventHTTP(ctx context.Context, in *deploymentEventInput) (*deliveryCommandOutput[DeploymentReceipt], error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	id, err := guid.RequireID(in.WorkItemID)
	if err != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	tenant := authn.TenantIDFromContext(ctx)
	return deliveryCommand(ctx, m, "work_item", defectLifecycleError, "work_item.deployment-event.create", http.MethodPost, deploymentPath, in.IdempotencyKey, 201, in.Body, map[string]string{"entity_id": project.String(), "work_item_id": id.String()}, func(tx *gorm.DB) (DeploymentReceipt, guid.ID, error) {
		v, e := m.recordDeploymentOn(ctx, tx, tenant, project, id, in.Body)
		return v, id, e
	}, func(tx *gorm.DB) error { _, e := m.deploymentWorkOn(ctx, tx, tenant, project, id); return e })
}
func (m *Module) defectReopenHTTP(ctx context.Context, in *defectReopenInput) (*deliveryCommandOutput[WorkView], error) {
	project, id, version, err := deliveryEditIDs(in.EntityID, in.WorkItemID, in.IfMatch)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	owner, err := m.preparationOwner(ctx, m.runtime.Database().Write(), tenant, project, id)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "work_item", defectLifecycleError, "work_item.defect-reopen.create", http.MethodPost, defectReopenPath, in.IdempotencyKey, 201, struct {
		Version int              `json:"version"`
		Body    defectReopenBody `json:"body"`
	}{version, in.Body}, map[string]string{"entity_id": project.String(), "work_item_id": id.String()}, func(tx *gorm.DB) (WorkView, guid.ID, error) {
		v, e := m.reopenDefectOn(ctx, tx, tenant, project, id, actor, owner, version, in.Body)
		return v, v.ID, e
	}, func(tx *gorm.DB) error {
		_, source, _, e := m.lockedWorkActorOn(ctx, tx, tenant, project, id, actor, owner, "work_item_write", true)
		if e != nil {
			return e
		}
		human, active, e := m.principals.ActiveHumanByID(ctx, tx, actor)
		if e != nil {
			return e
		}
		if !active || human.ID != actor {
			if source.State != "ACCEPTED" {
				return ErrWorkForbidden
			}
			return m.defectRoleOn(ctx, tx, tenant, project, actor, "agent_tester")
		}
		return nil
	})
}
