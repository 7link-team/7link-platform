package templates

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"strconv"
	"strings"
)

type bindingDiffInput struct {
	ID      string `path:"version_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Against string `query:"against" required:"true" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type bindingCreateInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		TemplateVersionID guid.ID `json:"template_version_id"`
		AccountableID     guid.ID `json:"accountable_principal_id"`
	}
}
type bindingOverrideInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	IfMatch        string `header:"If-Match" required:"true" maxLength:"32"`
	Body           struct {
		Overrides map[string]any `json:"overrides"`
	}
}
type bindingUpgradeInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	IfMatch        string `header:"If-Match" required:"true" maxLength:"32"`
	Body           struct {
		TargetVersionID     guid.ID `json:"target_version_id"`
		AcknowledgeBreaking bool    `json:"acknowledge_breaking"`
	}
}

func (m *Module) registerBindingHTTP(api huma.API) {
	guard := authz.Guard{Resource: "template", Verb: "read", FilterData: true}
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("template.diff", "GET", "/template-versions/{version_id}/diff", "Compare two versions of the same workflow template"), guard, m.bindingDiffHTTP)
	authz.Register(m.registrar, api, op("template.binding-get", "GET", "/entities/{project_id}/template-binding", "Read pinned template and effective approved parameters"), guard, m.instanceHTTP)
	authz.Register(m.registrar, api, op("template.binding-create", "PUT", "/entities/{project_id}/template-binding", "Bind an immutable published template to a project"), guard, m.bindingCreateHTTP)
	authz.Register(m.registrar, api, op("template.binding-overrides", "PATCH", "/entities/{project_id}/template-binding/brief-overrides", "Human approval of range checked Brief parameter overrides"), guard, m.bindingOverrideHTTP)
	authz.Register(m.registrar, api, op("template.binding-upgrade", "PUT", "/entities/{project_id}/template-binding/upgrade", "Approve a safe template upgrade preserving materialized work"), guard, m.bindingUpgradeHTTP)
}
func bindingVersionHeader(value string) (int, error) {
	if strings.HasPrefix(value, "\"") {
		var e error
		value, e = strconv.Unquote(value)
		if e != nil {
			return 0, ErrInvalid
		}
	}
	v, e := strconv.Atoi(value)
	if e != nil || v < 1 {
		return 0, ErrInvalid
	}
	return v, nil
}
func (m *Module) bindingDiffHTTP(ctx context.Context, in *bindingDiffInput) (*respx.Body[TemplateDiff], error) {
	id, e := guid.RequireID(in.ID)
	against, e2 := guid.RequireID(in.Against)
	if e != nil || e2 != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	var out TemplateDiff
	e = m.transaction(ctx, func(tx *gorm.DB) error {
		first, second := against, id
		if first > second {
			first, second = second, first
		}
		a, e := m.versionOn(ctx, tx, first, "template_read")
		if e != nil {
			return e
		}
		b, e := m.versionOn(ctx, tx, second, "template_read")
		if e != nil {
			return e
		}
		if first != against {
			a, b = b, a
		}
		if a.Key != b.Key {
			return ErrDiffMismatch
		}
		before, e := versionView(a)
		if e != nil {
			return e
		}
		after, e := versionView(b)
		if e != nil {
			return e
		}
		out, e = diffDefinitions(before.Definition, after.Definition)
		return e
	})
	if e != nil {
		return nil, templateError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) bindingCreateHTTP(ctx context.Context, in *bindingCreateInput) (*respx.Body[TemplateInstanceView], error) {
	project, e := guid.RequireID(in.ProjectID)
	if e != nil || in.Body.TemplateVersionID <= 0 || in.Body.AccountableID <= 0 {
		return nil, templateError(ctx, ErrInvalid)
	}
	return command(m, ctx, in.IdempotencyKey, "template.binding-create:"+in.ProjectID, in.Body, func(tx *gorm.DB) (TemplateInstanceView, error) {
		return m.instantiateOn(ctx, tx, in.Body.TemplateVersionID, project, in.Body.AccountableID)
	})
}
func (m *Module) bindingOverrideHTTP(ctx context.Context, in *bindingOverrideInput) (*respx.Body[TemplateInstanceView], error) {
	project, e := guid.RequireID(in.ProjectID)
	version, e2 := bindingVersionHeader(in.IfMatch)
	if e != nil || e2 != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	request := struct {
		Version   int
		Overrides map[string]any
	}{version, in.Body.Overrides}
	return command(m, ctx, in.IdempotencyKey, "template.binding-overrides:"+in.ProjectID, request, func(tx *gorm.DB) (TemplateInstanceView, error) {
		return m.approveBindingOverridesOn(ctx, tx, project, version, in.Body.Overrides)
	}, func(tx *gorm.DB) error { _, e := m.studioAuthorizeOn(ctx, tx, project); return e })
}
func (m *Module) bindingUpgradeHTTP(ctx context.Context, in *bindingUpgradeInput) (*respx.Body[TemplateInstanceView], error) {
	project, e := guid.RequireID(in.ProjectID)
	version, e2 := bindingVersionHeader(in.IfMatch)
	if e != nil || e2 != nil || in.Body.TargetVersionID <= 0 {
		return nil, templateError(ctx, ErrInvalid)
	}
	request := struct {
		Version int
		Target  guid.ID
		Ack     bool
	}{version, in.Body.TargetVersionID, in.Body.AcknowledgeBreaking}
	return command(m, ctx, in.IdempotencyKey, "template.binding-upgrade:"+in.ProjectID, request, func(tx *gorm.DB) (TemplateInstanceView, error) {
		return m.upgradeBindingOn(ctx, tx, project, in.Body.TargetVersionID, version, in.Body.AcknowledgeBreaking)
	}, func(tx *gorm.DB) error { _, e := m.studioAuthorizeOn(ctx, tx, project); return e })
}
