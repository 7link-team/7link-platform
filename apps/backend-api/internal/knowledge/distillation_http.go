package knowledge

import (
	"context"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type distillationCreateInput struct {
	KnowledgeTenantInput
	WorkItemID     string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           DistillationInput
}
type distillationGetInput struct {
	KnowledgeTenantInput
	DistillationID string `path:"distillation_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) registerDistillationHTTP(api huma.API) error {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.skill-distillations.create", Method: http.MethodPost, Path: "/work-items/{work_item_id}/skill-distillations", Summary: "Start a real accepted-work skill distillation for Human version review", Tags: []string{"work-items"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "write"}, func(ctx context.Context, in *distillationCreateInput) (*lifecycleOutput[model.Distillation], error) {
		work, err := guid.RequireID(in.WorkItemID)
		if err != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.skill-distillations.create", http.MethodPost, "/work-items/{work_item_id}/skill-distillations", map[string]string{"tenant_id": in.TenantID, "work_item_id": in.WorkItemID}, in.Body, 201,
			func(tx *gorm.DB, tenant guid.ID) (model.Distillation, error) {
				return m.startDistillationOn(ctx, tx, tenant, work, in.Body)
			},
			func(tx *gorm.DB, tenant guid.ID, row model.Distillation) error {
				return m.authorizeDistillationOn(ctx, tx, tenant, row)
			})
	})
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.skill-distillations.get", Method: http.MethodGet, Path: "/skill-distillations/{distillation_id}", Summary: "Read an accepted-work skill distillation and its draft version reference", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "write"}, func(ctx context.Context, in *distillationGetInput) (*respx.Body[model.Distillation], error) {
		id, err := guid.RequireID(in.DistillationID)
		if err != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		return lifecycleRead(ctx, m, in.TenantID, func(tx *gorm.DB, tenant guid.ID) (model.Distillation, error) {
			var row model.Distillation
			err := query.BindDistillations(tx, m.names).Where(map[string]any{"tenant_id": tenant, "id": id}).Take(&row).Error
			if err != nil {
				return row, skillDBError(err)
			}
			return row, m.authorizeDistillationOn(ctx, tx, tenant, row)
		})
	})
	return nil
}

func (m *Module) authorizeDistillationOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, row model.Distillation) error {
	if _, err := m.authorize(ctx, tx, tenant, Scope{Scope: "PROJECT", ProjectID: &row.ProjectID}, "knowledge_write", true); err != nil {
		return err
	}
	m.distillation.RLock()
	read := m.distillation.source.ReadOn
	m.distillation.RUnlock()
	if read == nil {
		return ErrUnavailable
	}
	fact, err := read(ctx, tx, tenant, row.WorkItemID)
	if err != nil {
		return err
	}
	if fact.ProjectID != row.ProjectID {
		return ErrForbidden
	}
	return nil
}
