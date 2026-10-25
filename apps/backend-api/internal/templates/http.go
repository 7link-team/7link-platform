package templates

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"strconv"
)

type getInput struct {
	ID string `path:"template_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type draftInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           struct {
		BasedOnVersionID guid.ID            `json:"based_on_version_id,omitempty"`
		Name             string             `json:"name,omitempty" maxLength:"200"`
		Key              string             `json:"key" maxLength:"64"`
		Definition       TemplateDefinition `json:"definition,omitempty"`
	}
}
type patchInput struct {
	ID             string `path:"template_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           TemplateWorkflowPatch
}
type publishInput struct {
	ID             string `path:"template_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           struct {
		Hash string `json:"hash" minLength:"64" maxLength:"64"`
	}
}
type instantiateInput struct {
	ID             string `path:"template_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           struct {
		ProjectID     guid.ID `json:"project_id"`
		AccountableID guid.ID `json:"accountable_principal_id"`
	}
}
type instanceInput struct {
	ProjectID string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type advanceInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
}
type listInput struct {
	TemplateFilters
	Cursor string `query:"cursor" maxLength:"128"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Sort   string `query:"sort" enum:"id" default:"id"`
}
type TemplateVersionPage struct {
	Items      []TemplateVersionView `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
	HasMore    bool                  `json:"has_more"`
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m.registrar == nil {
		return ErrUnavailable
	}
	m.registerRubricScoresHTTP(api)
	read := authz.Guard{Resource: "template", Verb: "read", FilterData: true}
	manage := authz.Guard{Resource: "template", Verb: "manage", FilterData: true}
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("template.verifier-plugin.list", "GET", "/verifier-plugins", "List installed verifier protocols without asserting service availability", 200), read, m.verifierPluginsHTTP)
	authz.Register(m.registrar, api, op("template.family-list", "GET", "/template-families", "List authorized workflow template families", 200), read, m.familiesHTTP)
	authz.Register(m.registrar, api, op("template.list", "GET", "/templates", "List workflow template versions", 200), read, m.listHTTP)
	authz.Register(m.registrar, api, op("template.draft", "POST", "/templates", "Create a workflow template draft or copy an immutable source version", 201), manage, m.draftHTTP)
	authz.Register(m.registrar, api, op("template.get", "GET", "/templates/{template_id}", "Read a workflow template version", 200), read, m.getHTTP)
	authz.Register(m.registrar, api, op("template.workflow-validate", "POST", "/templates/{template_id}/workflow-validations", "Validate a detached workflow against authorized subflows and inherited gates", 200), manage, m.workflowValidateHTTP)
	authz.Register(m.registrar, api, op("template.workflow-proposal", "POST", "/templates/{template_id}/workflow-proposals", "Generate a model workflow proposal for explicit Human review", 200), manage, m.workflowProposalHTTP)
	authz.Register(m.registrar, api, op("template.patch", "POST", "/templates/{template_id}/patches", "Apply a workflow patch to a draft", 200), manage, m.patchHTTP)
	authz.Register(m.registrar, api, op("template.publish", "POST", "/templates/{template_id}/publications", "Publish an immutable workflow version", 201), manage, m.publishHTTP)
	authz.Register(m.registrar, api, op("template.instantiate", "POST", "/templates/{template_id}/instances", "Instantiate real project work and a constitution draft", 201), read, m.instantiateHTTP)
	authz.Register(m.registrar, api, op("template.instance", "GET", "/projects/{project_id}/workflow", "Read a pinned instance and current work states", 200), read, m.instanceHTTP)
	authz.Register(m.registrar, api, op("template.advance", "POST", "/projects/{project_id}/workflow/advances", "Advance ready nodes using current authorized facts", 200), read, m.advanceHTTP)
	m.registerStudioHTTP(api)
	m.registerLifecycleHTTP(api)
	m.registerCalibrationHTTP(api)
	m.registerProviderHTTP(api)
	m.registerEvalHTTP(api)
	m.registerBindingHTTP(api)
	m.registerCopyHTTP(api)
	return m.registerWorkflowCommands(api)
}
func (m *Module) advanceHTTP(ctx context.Context, in *advanceInput) (*respx.Body[TemplateInstanceView], error) {
	id, e := guid.RequireID(in.ProjectID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return command(m, ctx, in.IdempotencyKey, "template.advance:"+in.ProjectID, in.ProjectID, func(tx *gorm.DB) (TemplateInstanceView, error) { return m.advanceOn(ctx, tx, id) })
}
func (m *Module) transaction(ctx context.Context, fn func(*gorm.DB) error) error {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return ErrUnavailable
	}
	return m.runtime.Database().Write().WithContext(ctx).Transaction(fn)
}
func templateError(ctx context.Context, e error) error {
	switch {
	case errors.Is(e, ErrTeamEvidence):
		return respx.ErrStatus(ctx, 409, 185461, "templates.error_team_evidence", nil)
	case errors.Is(e, ErrTeamIncomplete):
		return respx.ErrStatus(ctx, 409, 185462, "templates.error_team_incomplete", nil)
	case errors.Is(e, ErrTeamStatus):
		return respx.ErrStatus(ctx, 409, 185463, "templates.error_team_status", nil)
	case errors.Is(e, ErrProviderAdapter):
		return respx.ErrStatus(ctx, 422, 185450, "templates.error_provider_adapter", nil)
	case errors.Is(e, ErrProviderLicense):
		return respx.ErrStatus(ctx, 422, 185451, "templates.error_provider_license", nil)
	case errors.Is(e, ErrProviderBinding):
		return respx.ErrStatus(ctx, 422, 185452, "templates.error_provider_binding", nil)
	case errors.Is(e, ErrCalibrationSamples):
		return respx.ErrStatus(ctx, 422, 185441, "templates.error_calibration_samples", nil)
	case errors.Is(e, ErrCalibrationPending):
		return respx.ErrStatus(ctx, 409, 185442, "templates.error_calibration_pending", nil)
	case errors.Is(e, ErrCalibrationTarget):
		return respx.ErrStatus(ctx, 422, 185443, "templates.error_calibration_target", nil)
	case errors.Is(e, ErrKeyTaken):
		return respx.ErrStatus(ctx, 409, 185413, "templates.error_key_taken", nil)
	case errors.Is(e, ErrNoNewerVersion):
		return respx.ErrStatus(ctx, 409, 185414, "templates.error_no_newer_version", nil)
	case errors.Is(e, ErrMandatoryGate):
		return respx.ErrStatus(ctx, 422, 185424, "templates.error_mandatory_gate", nil)
	case errors.Is(e, ErrOverrideRange):
		return respx.ErrStatus(ctx, 422, 185430, "templates.error_override_out_of_range", nil)
	case errors.Is(e, ErrBreakingAck):
		return respx.ErrStatus(ctx, 422, 185417, "templates.error_breaking_unacknowledged", nil)
	case errors.Is(e, ErrDiffMismatch):
		return respx.ErrStatus(ctx, 422, 185431, "templates.error_diff_mismatch", nil)
	case errors.Is(e, intake.ErrForbidden), errors.Is(e, governance.ErrForbidden), errors.Is(e, ErrForbidden), errors.Is(e, delivery.ErrWorkForbidden):
		return respx.ErrStatus(ctx, 403, 185403, "templates.forbidden", nil)
	case errors.Is(e, ErrNotFound), errors.Is(e, delivery.ErrProjectNotFound), errors.Is(e, gorm.ErrRecordNotFound):
		return respx.ErrStatus(ctx, 404, 185404, "templates.not_found", nil)
	case errors.Is(e, intake.ErrConflict), errors.Is(e, governance.ErrVersion), errors.Is(e, ErrConflict), errors.Is(e, idempotency.ErrKeyReused):
		return respx.ErrStatus(ctx, 409, 185410, "templates.conflict", nil)
	case errors.Is(e, intake.ErrInvalidArgument), errors.Is(e, governance.ErrInvalidArgument), errors.Is(e, ErrInvalid), errors.Is(e, idempotency.ErrInvalidKey), errors.Is(e, delivery.ErrInvalidArgument):
		return respx.ErrStatus(ctx, 422, 185422, "templates.invalid", nil)
	case errors.Is(e, intake.ErrUnavailable), errors.Is(e, governance.ErrUnavailable), errors.Is(e, ErrUnavailable), errors.Is(e, delivery.ErrUnavailable):
		return respx.ErrStatus(ctx, 503, 185503, "templates.unavailable", nil)
	default:
		return respx.ErrStatus(ctx, 500, 185500, "templates.failed", nil)
	}
}
func command[T any](m *Module, ctx context.Context, key, operation string, input any, fn func(*gorm.DB) (T, error), authorize ...func(*gorm.DB) error) (*respx.Body[T], error) {
	var out T
	b, e := json.Marshal(input)
	if e != nil {
		return nil, templateError(ctx, e)
	}
	e = m.transaction(ctx, func(tx *gorm.DB) error {
		for _, check := range authorize {
			if err := check(tx); err != nil {
				return err
			}
		}
		r, _, e := m.idem.Do(ctx, tx, idempotency.Key{TenantID: authn.TenantIDFromContext(ctx), SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: key, RequestHash: sha256.Sum256(b)}, func(work *gorm.DB) (idempotency.Result, error) {
			v, e := fn(work)
			if e != nil {
				return idempotency.Result{}, e
			}
			var receipt any = v
			if instance, ok := any(v).(TemplateInstanceView); ok {
				receipt = TemplateInstanceView{ID: instance.ID, ProjectID: instance.ProjectID, TemplateVersionID: instance.TemplateVersionID, Hash: instance.Hash}
			}
			body, e := json.Marshal(receipt)
			status := 200
			switch operation {
			case "template.calibration-sample-create", "template.eval-case-create", "template.provider-create":
				status = 201
			}
			return idempotency.Result{StatusCode: status, Body: body}, e
		})
		if e != nil {
			return e
		}
		if e = json.Unmarshal(r.Body, &out); e != nil {
			return e
		}
		switch v := any(out).(type) {
		case TeamTaskView:
			current, e := m.teamReplayOn(ctx, tx, v)
			if e != nil {
				return e
			}
			currentOut, ok := any(current).(T)
			if !ok {
				return ErrInvalid
			}
			out = currentOut
		case EvalRunView:
			// Replay must retain current access to every project in the frozen
			// snapshot, even when a case was later deactivated or removed.
			if _, e = m.evalRunOn(ctx, tx, v.ID); e != nil {
				return e
			}
		case TemplateVersionView:
			permission := "template_manage"
			if operation == "template.get" {
				permission = "template_read"
			}
			if _, e = m.versionOn(ctx, tx, v.ID, permission); e != nil {
				return e
			}
		case TemplateInstanceView:
			if _, _, _, e = m.authorizeInstanceCommandOn(ctx, tx, v.ProjectID); e != nil {
				return e
			}
			current, err := m.instanceOn(ctx, tx, v.ProjectID)
			if err != nil {
				return err
			}
			if current.ID != v.ID || current.TemplateVersionID != v.TemplateVersionID || current.Hash != v.Hash {
				return ErrConflict
			}
			typed, ok := any(current).(T)
			if !ok {
				return ErrInvalid
			}
			out = typed
		}
		return nil
	})
	if e != nil {
		return nil, templateError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) draftHTTP(ctx context.Context, in *draftInput) (*respx.Body[TemplateVersionView], error) {
	return command(m, ctx, in.IdempotencyKey, "template.draft", in.Body, func(tx *gorm.DB) (TemplateVersionView, error) {
		if in.Body.BasedOnVersionID != 0 {
			if in.Body.Definition.Domain != "" || in.Body.Definition.Workflow.WorkflowID != "" || in.Body.Definition.Metadata != nil {
				return TemplateVersionView{}, ErrInvalid
			}
			return m.copyOn(ctx, tx, in.Body.BasedOnVersionID, in.Body.Key, in.Body.Name)
		}
		if in.Body.Name != "" {
			return TemplateVersionView{}, ErrInvalid
		}
		return m.draftOn(ctx, tx, in.Body.Key, in.Body.Definition)
	}, func(tx *gorm.DB) error {
		if in.Body.BasedOnVersionID != 0 {
			_, err := m.copySourceOn(ctx, tx, in.Body.BasedOnVersionID)
			return err
		}
		return nil
	})
}
func (m *Module) patchHTTP(ctx context.Context, in *patchInput) (*respx.Body[TemplateVersionView], error) {
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return command(m, ctx, in.IdempotencyKey, "template.patch:"+in.ID, in.Body, func(tx *gorm.DB) (TemplateVersionView, error) { return m.patchOn(ctx, tx, id, in.Body) })
}
func (m *Module) publishHTTP(ctx context.Context, in *publishInput) (*respx.Body[TemplateVersionView], error) {
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return command(m, ctx, in.IdempotencyKey, "template.publish:"+in.ID, in.Body, func(tx *gorm.DB) (TemplateVersionView, error) { return m.publishOn(ctx, tx, id, in.Body.Hash) })
}
func (m *Module) instantiateHTTP(ctx context.Context, in *instantiateInput) (*respx.Body[TemplateInstanceView], error) {
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return command(m, ctx, in.IdempotencyKey, "template.instantiate:"+in.ID, in.Body, func(tx *gorm.DB) (TemplateInstanceView, error) {
		return m.instantiateOn(ctx, tx, id, in.Body.ProjectID, in.Body.AccountableID)
	})
}
func (m *Module) getHTTP(ctx context.Context, in *getInput) (*respx.Body[TemplateVersionView], error) {
	var out TemplateVersionView
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	e = m.transaction(ctx, func(tx *gorm.DB) error {
		r, e := m.copySourceOn(ctx, tx, id)
		if e != nil {
			return e
		}
		out, e = versionView(r)
		return e
	})
	if e != nil {
		return nil, templateError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) instanceHTTP(ctx context.Context, in *instanceInput) (*respx.Body[TemplateInstanceView], error) {
	var out TemplateInstanceView
	id, e := guid.RequireID(in.ProjectID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	e = m.transaction(ctx, func(tx *gorm.DB) error { var e error; out, e = m.instanceOn(ctx, tx, id); return e })
	if e != nil {
		return nil, templateError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) listHTTP(ctx context.Context, in *listInput) (*respx.Body[TemplateVersionPage], error) {
	out := TemplateVersionPage{Items: []TemplateVersionView{}}
	after := int64(0)
	if in.Cursor != "" {
		n, e := strconv.ParseInt(in.Cursor, 10, 64)
		if e != nil || n < 0 {
			return nil, templateError(ctx, ErrInvalid)
		}
		after = n
	}
	if in.Limit < 1 || in.Limit > 100 || in.Sort != "id" || !in.TemplateFilters.valid() {
		return nil, templateError(ctx, ErrInvalid)
	}
	e := m.transaction(ctx, func(tx *gorm.DB) error {
		return m.scanLibraryOn(ctx, tx, in.TemplateFilters, guid.ID(after), "", func(v TemplateVersionView) bool {
			if len(out.Items) == in.Limit {
				out.HasMore = true
				return false
			}
			out.Items = append(out.Items, v)
			return true
		})
	})
	if e != nil {
		return nil, templateError(ctx, e)
	}
	if out.HasMore {
		out.NextCursor = out.Items[len(out.Items)-1].ID.String()
	}
	return respx.OK(ctx, out), nil
}
