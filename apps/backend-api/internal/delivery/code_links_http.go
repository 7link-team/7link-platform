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

const codeLinksPath = workPath + "/{work_item_id}/code-links"

type codeLinkCreateInput struct {
	ResultWorkInput
	Key  string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body CodeLinkInput
}
type codeLinkEvidenceInput struct {
	ResultWorkInput
	CodeLinkID string `path:"code_link_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) registerCodeLinksHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.code-link-list", Method: http.MethodGet, Path: codeLinksPath, Summary: "List immutable code references", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.listCodeLinksHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.code-link-create", Method: http.MethodPost, Path: codeLinksPath, Summary: "Record a manual code reference without asserting merge or deployment", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "write"}, m.createCodeLinkHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.code-link-evidence", Method: http.MethodGet, Path: codeLinksPath + "/{code_link_id}/evidence", Summary: "Read hash-verified code reference evidence", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.codeLinkEvidenceHTTP)
	if err := authz.RegisterRequestContract(api, codeLinksPath); err != nil {
		return err
	}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement.code-link-list", Method: http.MethodGet, Path: requirementCodeLinksPath, Summary: "List immutable requirement code references", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "read"}, m.listRequirementCodeLinksHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement.code-link-create", Method: http.MethodPost, Path: requirementCodeLinksPath, Summary: "Record a manual requirement code reference", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "write"}, m.createRequirementCodeLinkHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement.code-link-evidence", Method: http.MethodGet, Path: requirementCodeLinksPath + "/{code_link_id}/evidence", Summary: "Read hash-verified requirement code evidence", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "read"}, m.requirementCodeLinkEvidenceHTTP)
	return authz.RegisterRequestContract(api, requirementCodeLinksPath)
}
func (m *Module) listCodeLinksHTTP(ctx context.Context, in *ResultWorkInput) (*respx.Body[[]model.CodeLink], error) {
	tenant, project, work, actor, _, err := m.consumptionContext(ctx, *in)
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	rows := []model.CodeLink{}
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.readWork(ctx, tx, tenant, project, work, actor); e != nil {
			return e
		}
		return query.BindCodeLinks(tx, m.service.names).WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND target_kind = ? AND target_id = ?", tenant, project, "work_item", work).Order("id").Find(&rows).Error
	})
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	return respx.OK(ctx, rows), nil
}
func (m *Module) createCodeLinkHTTP(ctx context.Context, in *codeLinkCreateInput) (*deliveryCommandOutput[model.CodeLink], error) {
	tenant, project, work, actor, owner, err := m.consumptionContext(ctx, in.ResultWorkInput)
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "work_item", resultHTTPError, "work_item.code-link-create", http.MethodPost, codeLinksPath, in.Key, 201, in.Body, map[string]string{"entity_id": project.String(), "work_item_id": work.String()}, func(tx *gorm.DB) (model.CodeLink, guid.ID, error) {
		row, e := m.createCodeLinkOn(ctx, tx, tenant, project, work, actor, owner, in.Body, "MANUAL_REFERENCE")
		return row, work, e
	}, func(tx *gorm.DB) error {
		_, _, _, e := m.lockedWorkOn(ctx, tx, tenant, project, work, actor, owner, "work_item_write")
		return e
	})
}
func (m *Module) codeLinkEvidenceHTTP(ctx context.Context, in *codeLinkEvidenceInput) (*respx.Body[CodeLinkEvidence], error) {
	tenant, project, work, actor, _, err := m.consumptionContext(ctx, in.ResultWorkInput)
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	id, err := guid.RequireID(in.CodeLinkID)
	if err != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	var row CodeLinkEvidence
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		row, e = m.CodeLinkEvidenceOn(ctx, tx, tenant, project, work, id, actor)
		return e
	})
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	return respx.OK(ctx, row), nil
}

const requirementCodeLinksPath = requirementPath + "/{requirement_id}/code-links"

type requirementCodeLinkInput struct {
	RequirementGetInput
	Key  string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body CodeLinkInput
}
type requirementCodeEvidenceInput struct {
	RequirementGetInput
	CodeLinkID string `path:"code_link_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) requirementCodeContext(ctx context.Context, in RequirementGetInput) (guid.ID, guid.ID, guid.ID, guid.ID, error) {
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	project, e := guid.RequireID(in.EntityID)
	id, e2 := guid.RequireID(in.RequirementID)
	if e != nil || e2 != nil {
		return 0, 0, 0, 0, ErrInvalidArgument
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return 0, 0, 0, 0, ErrUnavailable
	}
	return tenant, project, id, actor, nil
}
func (m *Module) listRequirementCodeLinksHTTP(ctx context.Context, in *RequirementGetInput) (*respx.Body[[]model.CodeLink], error) {
	tenant, project, id, actor, err := m.requirementCodeContext(ctx, *in)
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	rows := []model.CodeLink{}
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.readRequirement(ctx, tx, tenant, project, id, actor); e != nil {
			return e
		}
		return query.BindCodeLinks(tx, m.service.names).WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND target_kind = ? AND target_id = ?", tenant, project, "requirement", id).Order("id").Find(&rows).Error
	})
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	return respx.OK(ctx, rows), nil
}
func (m *Module) createRequirementCodeLinkHTTP(ctx context.Context, in *requirementCodeLinkInput) (*deliveryCommandOutput[model.CodeLink], error) {
	tenant, project, id, actor, err := m.requirementCodeContext(ctx, in.RequirementGetInput)
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "requirement", requirementHTTPError, "requirement.code-link-create", http.MethodPost, requirementCodeLinksPath, in.Key, 201, in.Body, map[string]string{"entity_id": project.String(), "requirement_id": id.String()}, func(tx *gorm.DB) (model.CodeLink, guid.ID, error) {
		row, e := m.createRequirementCodeLinkOn(ctx, tx, tenant, project, id, actor, in.Body, "MANUAL_REFERENCE")
		return row, id, e
	}, func(tx *gorm.DB) error {
		q := query.BindRequirements(tx, m.service.names)
		row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.ArchivedAt.IsNull()).First()
		if e != nil {
			return e
		}
		return m.requirementReviewWriterOn(ctx, tx, row, actor)
	})
}
func (m *Module) requirementCodeLinkEvidenceHTTP(ctx context.Context, in *requirementCodeEvidenceInput) (*respx.Body[CodeLinkEvidence], error) {
	tenant, project, id, actor, err := m.requirementCodeContext(ctx, in.RequirementGetInput)
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	evidenceID, err := guid.RequireID(in.CodeLinkID)
	if err != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	var row CodeLinkEvidence
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		row, e = m.RequirementCodeLinkEvidenceOn(ctx, tx, tenant, project, id, evidenceID, actor)
		return e
	})
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	return respx.OK(ctx, row), nil
}
