package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"slices"
)

const entityBase = "/entities/{entity_id}"

func (m *Module) registerDomainHTTP(api huma.API) error {
	m.registerPackageCommentsHTTP(api)
	m.registerTestCaseImportHTTP(api)
	m.registerPackageReworkHTTP(api)
	owner := authz.ResourceOwnerResolver(m.owner)
	guard := authz.Guard{Resource: "channel", Verb: "read"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.preview.source-candidate", Method: "GET", Path: entityBase + "/artifact-previews/source-candidates/{work_item_id}/{file_id}", Summary: "Resolve an authorized pre-result source file version", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.progressSourceHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.change.create", Method: "POST", Path: entityBase + "/client-changes", Summary: "Create change", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.create_changeHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.issue.create", Method: "POST", Path: entityBase + "/client-issues", Summary: "Create issue", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.create_issueHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package.create", Method: "POST", Path: entityBase + "/delivery-packages", Summary: "Create package", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.create_packageHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package.edit", Method: "PATCH", Path: entityBase + "/delivery-packages/{id}", Summary: "Edit a draft package and revalidate its fixed artifacts", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.editPackageHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.preview.create", Method: "POST", Path: entityBase + "/artifact-previews", Summary: "Create preview", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.create_previewHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.annotation.create", Method: "POST", Path: entityBase + "/artifact-comments", Summary: "Create annotation", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.create_annotationHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_case.create", Method: "POST", Path: entityBase + "/test-cases", Summary: "Create test case", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.create_test_caseHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_plan.create", Method: "POST", Path: entityBase + "/test-plans", Summary: "Create test plan", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.create_test_planHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.change.command", Method: "POST", Path: entityBase + "/client-changes/{id}/actions", Summary: "change", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.changeHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.issue.command", Method: "POST", Path: entityBase + "/client-issues/{id}/actions", Summary: "issue", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.issueHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package_publish.command", Method: "POST", Path: entityBase + "/delivery-packages/{id}/publish", Summary: "package publish", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.package_publishHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package_acceptance.command", Method: "POST", Path: entityBase + "/delivery-packages/{id}/receipts", Summary: "package acceptance", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.package_acceptanceHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_case_edit.command", Method: "PATCH", Path: entityBase + "/test-cases/{id}", Summary: "test case edit", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.test_case_editHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_run.command", Method: "POST", Path: entityBase + "/test-plans/{id}/runs", Summary: "test run", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.test_runHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.change.list", Method: "GET", Path: entityBase + "/client-changes", Summary: "List authorized change records", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.list_changeHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.change.get", Method: "GET", Path: entityBase + "/client-changes/{id}", Summary: "Read an authorized change", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.get_changeHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.issue.list", Method: "GET", Path: entityBase + "/client-issues", Summary: "List authorized issue records", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.list_issueHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.issue.get", Method: "GET", Path: entityBase + "/client-issues/{id}", Summary: "Read an authorized issue", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.get_issueHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package.list", Method: "GET", Path: entityBase + "/delivery-packages", Summary: "List authorized package records", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.list_packageHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package.get", Method: "GET", Path: entityBase + "/delivery-packages/{id}", Summary: "Read an authorized package", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.get_packageHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.preview.list", Method: "GET", Path: entityBase + "/artifact-previews", Summary: "List authorized preview records", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.list_previewHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.preview.get", Method: "GET", Path: entityBase + "/artifact-previews/{id}", Summary: "Read an authorized preview", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.get_previewHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.annotation.list", Method: "GET", Path: entityBase + "/artifact-comments", Summary: "List authorized annotation records", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.list_annotationHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.annotation.get", Method: "GET", Path: entityBase + "/artifact-comments/{id}", Summary: "Read an authorized annotation", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.get_annotationHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_case.list", Method: "GET", Path: entityBase + "/test-cases", Summary: "List authorized test case records", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.list_test_caseHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_case.get", Method: "GET", Path: entityBase + "/test-cases/{id}", Summary: "Read an authorized test case", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.get_test_caseHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_plan.list", Method: "GET", Path: entityBase + "/test-plans", Summary: "List authorized test plan records", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.list_test_planHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_plan.get", Method: "GET", Path: entityBase + "/test-plans/{id}", Summary: "Read an authorized test plan", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.get_test_planHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_run.list", Method: "GET", Path: entityBase + "/test-runs", Summary: "List authorized test run records", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.list_test_runHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_run.get", Method: "GET", Path: entityBase + "/test-runs/{id}", Summary: "Read an authorized test run", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.get_test_runHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.settings.get", Method: "GET", Path: entityBase + "/project/settings", Summary: "Get editable project settings", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "manage"}, m.getSettingsHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.settings.patch", Method: "PATCH", Path: entityBase + "/project/settings", Summary: "Patch editable project settings", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "manage"}, m.patchSettingsHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	if e := authz.RegisterRequestContract(api, entityBase+"/delivery-packages/{id}/rework"); e != nil {
		return e
	}
	for _, path := range []string{entityBase + "/delivery-packages/{id}/items/{item_hash}/comments", entityBase + "/artifact-comments", entityBase + "/artifact-previews", entityBase + "/client-changes", entityBase + "/client-changes/{id}/actions", entityBase + "/client-issues", entityBase + "/client-issues/{id}/actions", entityBase + "/delivery-packages", entityBase + "/delivery-packages/{id}", entityBase + "/delivery-packages/{id}/publish", entityBase + "/delivery-packages/{id}/receipts", entityBase + "/project/settings", entityBase + "/test-cases", entityBase + "/test-cases/{id}", entityBase + "/test-plans", entityBase + "/test-plans/{id}/runs"} {
		if e := authz.RegisterRequestContract(api, path); e != nil {
			return e
		}
	}
	return nil
}
func (m *Module) create_changeHTTP(ctx context.Context, in *CreateInput[ChangeBody]) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.change.create", "POST", entityBase+"/client-changes", in.Key, 201, in.Body, "requirement_read", true, nil, func(tx *gorm.DB) (Receipt, error) { return m.createChangeOn(ctx, tx, p, in.Body) })
}
func (m *Module) create_issueHTTP(ctx context.Context, in *CreateInput[IssueBody]) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.issue.create", "POST", entityBase+"/client-issues", in.Key, 201, in.Body, "project_read", true, nil, func(tx *gorm.DB) (Receipt, error) { return m.createIssueOn(ctx, tx, p, in.Body) })
}
func (m *Module) create_packageHTTP(ctx context.Context, in *CreateInput[PackageBody]) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.package.create", "POST", entityBase+"/delivery-packages", in.Key, 201, in.Body, "delivery_compose", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.createPackageOn(ctx, tx, p, in.Body) })
}
func (m *Module) create_previewHTTP(ctx context.Context, in *CreateInput[PreviewBody]) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.preview.create", "POST", entityBase+"/artifact-previews", in.Key, 201, in.Body, "project_manage", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.createPreviewOn(ctx, tx, p, in.Body) })
}
func (m *Module) create_annotationHTTP(ctx context.Context, in *CreateInput[AnnotationBody]) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.annotation.create", "POST", entityBase+"/artifact-comments", in.Key, 201, in.Body, "artifact_comment_write", true, nil, func(tx *gorm.DB) (Receipt, error) { return m.createAnnotationOn(ctx, tx, p, in.Body) })
}
func (m *Module) create_test_caseHTTP(ctx context.Context, in *CreateInput[TestCaseBody]) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.test_case.create", "POST", entityBase+"/test-cases", in.Key, 201, in.Body, "test_case_manage", false, func(tx *gorm.DB) error {
		if in.Body.IsHiddenAcceptance {
			return m.requireHiddenAcceptanceOn(ctx, tx, p)
		}
		return nil
	}, func(tx *gorm.DB) (Receipt, error) { return m.writeCaseOn(ctx, tx, p, 0, 0, in.Body) })
}
func (m *Module) create_test_planHTTP(ctx context.Context, in *CreateInput[TestPlanBody]) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.test_plan.create", "POST", entityBase+"/test-plans", in.Key, 201, in.Body, "test_plan_manage", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.createPlanOn(ctx, tx, p, in.Body) })
}
func (m *Module) changeHTTP(ctx context.Context, in *EditInput[ChangeAction]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.change.command", "POST", entityBase+"/client-changes/"+id.String()+"/actions", in.Key, 200, in, "requirement_write", true, func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, "requirement_write", true)
		if e != nil {
			return e
		}
		_, e = m.changeOn(ctx, tx, p, id, ext)
		return e
	}, func(tx *gorm.DB) (Receipt, error) { return m.actChangeOn(ctx, tx, p, id, v, in.Body) })
}
func (m *Module) issueHTTP(ctx context.Context, in *EditInput[IssueAction]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.issue.command", "POST", entityBase+"/client-issues/"+id.String()+"/actions", in.Key, 200, in, "project_manage", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.actIssueOn(ctx, tx, p, id, v, in.Body) })
}
func (m *Module) package_publishHTTP(ctx context.Context, in *EditInput[PackagePublishBody]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	q := query.BindDeliveryPackage(m.writer(), m.names)
	candidate, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id)).Take()
	if e != nil {
		return nil, httpError(ctx, ErrNotFound)
	}
	return m.command(ctx, p, "clientwork.package_publish.command", "POST", entityBase+"/delivery-packages/"+id.String()+"/publish", in.Key, 200, in, "delivery_send", false, func(tx *gorm.DB) error {
		row, err := m.packageOn(ctx, tx, p, id, false)
		if err != nil {
			return err
		}
		if row.RecipientID != candidate.RecipientID {
			return ErrConflict
		}
		if m.packageRecipient == nil {
			return ErrUnavailable
		}
		if err := m.packageRecipient(ctx, tx, row.TenantID, p, row.RecipientID); err != nil {
			return err
		}
		active, err := m.clients.IsClientOn(ctx, tx, row.TenantID, p, row.RecipientID)
		if err != nil {
			return err
		}
		if !active {
			return ErrNotFound
		}
		return nil
	}, func(tx *gorm.DB) (Receipt, error) {
		if m.acceptanceProof == nil {
			return Receipt{}, ErrUnavailable
		}
		if in.Body.ReauthProof == "" {
			return Receipt{}, authn.ErrInvalidCredentials
		}
		if e := m.acceptanceProof(ctx, tx, authn.PrincipalIDFromContext(ctx), in.Cookie, in.Body.ReauthProof, "delivery_send", "package:"+id.String()); e != nil {
			return Receipt{}, e
		}
		return m.publishPackageOn(ctx, tx, p, id, v)
	}, func(tx *gorm.DB) error { return m.lockRecordedParticipantsOn(ctx, tx, candidate.RecipientID) })
}

func (m *Module) package_acceptanceHTTP(ctx context.Context, in *EditInput[PackageDecision]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.package_acceptance.command", "POST", entityBase+"/delivery-packages/"+id.String()+"/receipts", in.Key, 200, in, "delivery_read", true, func(tx *gorm.DB) error {
		if _, err := m.clientSignerOn(ctx, tx, p); err != nil {
			return err
		}
		ext, e := m.authorize(ctx, tx, p, "delivery_read", true)
		if e != nil {
			return e
		}
		if !ext {
			return ErrForbidden
		}
		_, e = m.packageOn(ctx, tx, p, id, true)
		return e
	}, func(tx *gorm.DB) (Receipt, error) { return m.decidePackageOn(ctx, tx, p, id, v, in.Body) })
}
func (m *Module) test_case_editHTTP(ctx context.Context, in *EditInput[TestCaseBody]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.test_case_edit.command", "PATCH", entityBase+"/test-cases/"+id.String()+"", in.Key, 200, in, "test_case_manage", false, func(tx *gorm.DB) error {
		row, err := m.caseOn(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if row.IsHiddenAcceptance || in.Body.IsHiddenAcceptance {
			return m.requireHiddenAcceptanceOn(ctx, tx, p)
		}
		return nil
	}, func(tx *gorm.DB) (Receipt, error) { return m.writeCaseOn(ctx, tx, p, id, v, in.Body) })
}
func (m *Module) test_runHTTP(ctx context.Context, in *EditInput[TestRunBody]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.test_run.command", "POST", entityBase+"/test-plans/"+id.String()+"/runs", in.Key, 200, in, "test_run_execute", false, func(tx *gorm.DB) error {
		if in.Body.Defect == nil {
			return nil
		}
		return m.projects.LockProjectOn(ctx, tx, authn.TenantIDFromContext(ctx), p, "work_item_write")
	}, func(tx *gorm.DB) (Receipt, error) { return m.recordRunOn(ctx, tx, p, id, v, in.Body) }, func(tx *gorm.DB) error {
		if in.Body.Defect == nil {
			return nil
		}
		return m.lockRecordedParticipantsOn(ctx, tx, in.Body.Defect.AccountablePrincipalID)
	})
}

type SettingsEditInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true"`
	IfMatch  string `header:"If-Match" required:"true"`
	Body     delivery.ClientSettingsPatch
}

func (m *Module) getSettingsHTTP(ctx context.Context, in *ListInput) (*respx.Body[delivery.ClientSettings], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out delivery.ClientSettings
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.authorize(ctx, tx, p, "project_manage", false); e != nil {
			return e
		}
		var e error
		out, e = m.projects.SettingsOn(ctx, tx, authn.TenantIDFromContext(ctx), p, nil, 0)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) patchSettingsHTTP(ctx context.Context, in *SettingsEditInput) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	v, e := version(in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "project.settings.patch", "PATCH", entityBase+"/project/settings", in.Key, 200, in, "project_manage", false, nil, func(tx *gorm.DB) (Receipt, error) {
		if in.Body.TargetDate != nil {
			q := query.BindTolerance(tx, m.names)
			open, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.OpenExceptionID.IsNotNull()).Count()
			if e != nil {
				return Receipt{}, e
			}
			if open != 0 {
				return Receipt{}, ErrOpenException
			}
		}
		r, e := m.projects.SettingsOn(ctx, tx, authn.TenantIDFromContext(ctx), p, &in.Body, v)
		if e != nil {
			return Receipt{}, e
		}
		if e = m.event(ctx, tx, p, p, "settings_changed", "project_manage", r.Version); e != nil {
			return Receipt{}, e
		}
		return Receipt{ID: p, Version: r.Version, State: "UPDATED"}, nil
	}, func(tx *gorm.DB) error {
		ids := append(in.Body.MemberPrincipals(), authn.PrincipalIDFromContext(ctx))
		slices.Sort(ids)
		ids = slices.Compact(ids)
		if m.principals.Lock == nil {
			return ErrUnavailable
		}
		for _, id := range ids {
			if id <= 0 {
				return ErrInvalid
			}
			if e := m.principals.Lock(ctx, tx, id); e != nil {
				return e
			}
		}
		return nil
	})
}
func readDomain[T any](ctx context.Context, m *Module, in *ListInput, kind, permission string, clients bool, selectRows func(*gorm.DB, bool, guid.ID, int) ([]T, []guid.ID, error)) (*respx.Body[Page[T]], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil || in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	out := Page[T]{Items: []T{}}
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, permission, clients)
		if e != nil {
			return e
		}
		scope := cursorScope(ctx, p, kind)
		if ext {
			scope += ":client"
		}
		last, e := position(in.Cursor, scope)
		if e != nil {
			return e
		}
		rows, ids, e := selectRows(tx, ext, last, in.Limit+1)
		if e != nil {
			return e
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			out.NextCursor = cursor(scope, ids[in.Limit-1])
			rows = rows[:in.Limit]
		}
		out.Items = rows
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) list_changeHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[ChangeView]], error) {
	p, _ := guid.RequireID(in.EntityID)
	return readDomain(ctx, m, in, "change", "requirement_read", true, func(tx *gorm.DB, ext bool, last guid.ID, limit int) ([]ChangeView, []guid.ID, error) {
		q := query.BindChangeRequest(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p))
		if ext {
			r = r.Where(q.ActorID.Eq(authn.PrincipalIDFromContext(ctx)))
		}
		if last > 0 {
			r = r.Where(q.ID.Lt(last))
		}
		rows, e := r.Order(q.ID.Desc()).Limit(limit).Find()
		if e != nil {
			return nil, nil, e
		}
		out := []ChangeView{}
		ids := []guid.ID{}
		for _, row := range rows {
			v := changeView(row)
			out = append(out, v)
			ids = append(ids, row.ID)
		}
		return out, ids, nil
	})
}
func (m *Module) get_changeHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[ChangeView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out ChangeView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, "requirement_read", true)
		if e != nil {
			return e
		}
		_ = ext
		q := query.BindChangeRequest(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
		if ext {
			r = r.Where(q.ActorID.Eq(authn.PrincipalIDFromContext(ctx)))
		}
		row, e := r.First()
		if e != nil {
			return e
		}
		out = changeView(row)
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) list_issueHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[IssueView]], error) {
	p, _ := guid.RequireID(in.EntityID)
	return readDomain(ctx, m, in, "issue", "project_read", true, func(tx *gorm.DB, ext bool, last guid.ID, limit int) ([]IssueView, []guid.ID, error) {
		q := query.BindClientIssue(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p))
		if ext {
			r = r.Where(q.ActorID.Eq(authn.PrincipalIDFromContext(ctx)))
		}
		if last > 0 {
			r = r.Where(q.ID.Lt(last))
		}
		rows, e := r.Order(q.ID.Desc()).Limit(limit).Find()
		if e != nil {
			return nil, nil, e
		}
		out := []IssueView{}
		ids := []guid.ID{}
		for _, row := range rows {
			v, e := m.issueView(ctx, tx, row, "")
			if e != nil {
				return nil, nil, e
			}
			out = append(out, v)
			ids = append(ids, row.ID)
		}
		return out, ids, nil
	})
}
func (m *Module) get_issueHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[IssueView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out IssueView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, "project_read", true)
		if e != nil {
			return e
		}
		_ = ext
		q := query.BindClientIssue(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
		if ext {
			r = r.Where(q.ActorID.Eq(authn.PrincipalIDFromContext(ctx)))
		}
		row, e := r.First()
		if e != nil {
			return e
		}
		out, e = m.issueView(ctx, tx, row, in.ReplyCursor)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) list_packageHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[PackageView]], error) {
	p, _ := guid.RequireID(in.EntityID)
	return readDomain(ctx, m, in, "package", "delivery_read", true, func(tx *gorm.DB, ext bool, last guid.ID, limit int) ([]PackageView, []guid.ID, error) {
		q := query.BindDeliveryPackage(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p))
		if ext {
			r = r.Where(q.RecipientID.Eq(authn.PrincipalIDFromContext(ctx)), q.State.Neq("DRAFT"))
		}
		if last > 0 {
			r = r.Where(q.ID.Lt(last))
		}
		rows, e := r.Order(q.ID.Desc()).Limit(limit).Find()
		if e != nil {
			return nil, nil, e
		}
		out := []PackageView{}
		ids := []guid.ID{}
		for _, row := range rows {
			v, e := m.packageView(ctx, tx, row)
			if e != nil {
				return nil, nil, e
			}
			out = append(out, v)
			ids = append(ids, row.ID)
		}
		return out, ids, nil
	})
}
func (m *Module) get_packageHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[PackageView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out PackageView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, "delivery_read", true)
		if e != nil {
			return e
		}
		_ = ext
		q := query.BindDeliveryPackage(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
		if ext {
			r = r.Where(q.RecipientID.Eq(authn.PrincipalIDFromContext(ctx)), q.State.Neq("DRAFT"))
		}
		row, e := r.First()
		if e != nil {
			return e
		}
		out, e = m.packageView(ctx, tx, row)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) list_previewHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[PreviewView]], error) {
	p, _ := guid.RequireID(in.EntityID)
	return readDomain(ctx, m, in, "preview", "project_read", true, func(tx *gorm.DB, ext bool, last guid.ID, limit int) ([]PreviewView, []guid.ID, error) {
		q := query.BindPreview(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p))
		if ext {
			r = r.Where(q.ClientVisible.Is(true))
		}
		if last > 0 {
			r = r.Where(q.ID.Lt(last))
		}
		rows, e := r.Order(q.ID.Desc()).Limit(limit).Find()
		if e != nil {
			return nil, nil, e
		}
		out := []PreviewView{}
		ids := []guid.ID{}
		for _, row := range rows {
			v := previewView(row)
			out = append(out, v)
			ids = append(ids, row.ID)
		}
		return out, ids, nil
	})
}
func (m *Module) get_previewHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[PreviewView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out PreviewView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, "project_read", true)
		if e != nil {
			return e
		}
		_ = ext
		q := query.BindPreview(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
		if ext {
			r = r.Where(q.ClientVisible.Is(true))
		}
		row, e := r.First()
		if e != nil {
			return e
		}
		out = previewView(row)
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) list_annotationHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[AnnotationView]], error) {
	p, _ := guid.RequireID(in.EntityID)
	return readDomain(ctx, m, in, "annotation", "project_read", true, func(tx *gorm.DB, ext bool, last guid.ID, limit int) ([]AnnotationView, []guid.ID, error) {
		q := query.BindAnnotation(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p))
		if ext {
			r = r.Where(q.ClientVisible.Is(true)).Where(q.WithContext(ctx).Columns(q.PreviewID).In(visiblePreviews(ctx, m, tx, p)))
		}
		if last > 0 {
			r = r.Where(q.ID.Lt(last))
		}
		rows, e := r.Order(q.ID.Desc()).Limit(limit).Find()
		if e != nil {
			return nil, nil, e
		}
		out := []AnnotationView{}
		ids := []guid.ID{}
		for _, row := range rows {
			v, e := annotationView(row)
			if e != nil {
				return nil, nil, e
			}
			out = append(out, v)
			ids = append(ids, row.ID)
		}
		return out, ids, nil
	})
}
func (m *Module) get_annotationHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[AnnotationView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out AnnotationView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, "project_read", true)
		if e != nil {
			return e
		}
		_ = ext
		q := query.BindAnnotation(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
		if ext {
			r = r.Where(q.ClientVisible.Is(true)).Where(q.WithContext(ctx).Columns(q.PreviewID).In(visiblePreviews(ctx, m, tx, p)))
		}
		row, e := r.First()
		if e != nil {
			return e
		}
		out, e = annotationView(row)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) list_test_caseHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[TestCaseView]], error) {
	p, _ := guid.RequireID(in.EntityID)
	return readDomain(ctx, m, in, "test_case", "test_case_manage", false, func(tx *gorm.DB, ext bool, last guid.ID, limit int) ([]TestCaseView, []guid.ID, error) {
		q := query.BindTestCase(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p))
		allowed, e := m.hiddenAcceptanceAllowedOn(ctx, tx, p)
		if e != nil {
			return nil, nil, e
		}
		if !allowed {
			r = r.Where(q.IsHiddenAcceptance.Is(false))
		}
		if last > 0 {
			r = r.Where(q.ID.Lt(last))
		}
		rows, e := r.Order(q.ID.Desc()).Limit(limit).Find()
		if e != nil {
			return nil, nil, e
		}
		out := []TestCaseView{}
		ids := []guid.ID{}
		for _, row := range rows {
			v := caseView(row)
			out = append(out, v)
			ids = append(ids, row.ID)
		}
		return out, ids, nil
	})
}
func (m *Module) get_test_caseHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[TestCaseView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out TestCaseView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, "test_case_manage", false)
		if e != nil {
			return e
		}
		_ = ext
		q := query.BindTestCase(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
		row, e := r.First()
		if e != nil {
			return e
		}
		if row.IsHiddenAcceptance {
			if e := m.requireHiddenAcceptanceOn(ctx, tx, p); e != nil {
				return e
			}
		}
		out = caseView(row)
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) list_test_planHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[TestPlanView]], error) {
	p, _ := guid.RequireID(in.EntityID)
	return readDomain(ctx, m, in, "test_plan", "test_plan_manage", false, func(tx *gorm.DB, ext bool, last guid.ID, limit int) ([]TestPlanView, []guid.ID, error) {
		q := query.BindTestPlan(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p))
		if last > 0 {
			r = r.Where(q.ID.Lt(last))
		}
		rows, e := r.Order(q.ID.Desc()).Limit(limit).Find()
		if e != nil {
			return nil, nil, e
		}
		out := []TestPlanView{}
		ids := []guid.ID{}
		for _, row := range rows {
			v, e := planView(row)
			if e != nil {
				return nil, nil, e
			}
			out = append(out, v)
			ids = append(ids, row.ID)
		}
		return out, ids, nil
	})
}
func (m *Module) get_test_planHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[TestPlanView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out TestPlanView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, "test_plan_manage", false)
		if e != nil {
			return e
		}
		_ = ext
		q := query.BindTestPlan(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
		row, e := r.First()
		if e != nil {
			return e
		}
		out, e = planView(row)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) list_test_runHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[TestRunView]], error) {
	p, _ := guid.RequireID(in.EntityID)
	return readDomain(ctx, m, in, "test_run", "test_run_execute", false, func(tx *gorm.DB, ext bool, last guid.ID, limit int) ([]TestRunView, []guid.ID, error) {
		q := query.BindTestRun(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p))
		if last > 0 {
			r = r.Where(q.ID.Lt(last))
		}
		rows, e := r.Order(q.ID.Desc()).Limit(limit).Find()
		if e != nil {
			return nil, nil, e
		}
		out := []TestRunView{}
		ids := []guid.ID{}
		for _, row := range rows {
			v := runView(row)
			out = append(out, v)
			ids = append(ids, row.ID)
		}
		return out, ids, nil
	})
}
func (m *Module) get_test_runHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[TestRunView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out TestRunView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, "test_run_execute", false)
		if e != nil {
			return e
		}
		_ = ext
		q := query.BindTestRun(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
		row, e := r.First()
		if e != nil {
			return e
		}
		out = runView(row)
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func annotationView(row *model.Annotation) (AnnotationView, error) {
	v := AnnotationView{ID: row.ID, PreviewID: row.PreviewID, FileVersionID: row.FileVersionID, ContentHash: row.ContentHash, Body: row.Body, ClientVisible: row.ClientVisible}
	e := json.Unmarshal([]byte(row.Anchor), &v.Anchor)
	return v, e
}
func visiblePreviews(ctx context.Context, m *Module, tx *gorm.DB, p guid.ID) query.IPreviewDo {
	q := query.BindPreview(tx, m.names)
	return q.WithContext(ctx).Select(q.ID).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ClientVisible.Is(true))
}

func (m *Module) editPackageHTTP(ctx context.Context, in *EditInput[PackageBody]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.package.edit", "PATCH", entityBase+"/delivery-packages/"+id.String(), in.Key, 200, in, "delivery_compose", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.editPackageOn(ctx, tx, p, id, v, in.Body) })
}
