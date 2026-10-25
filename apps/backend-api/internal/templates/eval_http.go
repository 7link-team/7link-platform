package templates

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type evalListInput struct {
	TemplateKey string `query:"template_key" maxLength:"64"`
	Origin      string `query:"origin" enum:"SEED,MANUAL,ESCAPE"`
	Active      string `query:"active" enum:"true,false"`
	Cursor      string `query:"cursor" maxLength:"19"`
	Limit       int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Sort        string `query:"sort" enum:"id" default:"id"`
}
type EvalCasePage struct {
	Items      []EvalCaseView `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
}
type EvalRunPage struct {
	Items      []EvalRunView `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}
type evalCaseCreateInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           EvalCaseDraft
}
type evalCasePatchInput struct {
	ID             string `path:"case_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true" maxLength:"32"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           EvalCasePatch
}
type evalStartInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           EvalStart
}
type evalGetInput struct {
	ID string `path:"run_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) registerEvalHTTP(api huma.API) {
	read := authz.Guard{Resource: "template", Verb: "read", FilterData: true}
	manage := authz.Guard{Resource: "template", Verb: "manage", FilterData: true}
	op := func(id, method, path, tag, summary string) huma.Operation {
		status := 200
		if id == "template.eval-case-create" {
			status = 201
		}
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{tag}, Summary: summary, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("template.eval-case-list", "GET", "/eval-cases", "eval-cases", "List authorized evaluation cases with source provenance"), read, m.evalListHTTP)
	authz.Register(m.registrar, api, op("template.eval-case-create", "POST", "/eval-cases", "eval-cases", "Create a Human labelled case with pinned file versions"), manage, m.evalCreateHTTP) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	authz.Register(m.registrar, api, op("template.eval-case-patch", "PATCH", "/eval-cases/{case_id}", "eval-cases", "Revise case labels or activity without changing prior run snapshots"), manage, m.evalPatchHTTP)
	authz.Register(m.registrar, api, op("template.eval-run-list", "GET", "/eval-runs", "eval-runs", "List immutable evaluation run snapshots and actual results"), read, m.evalRunsHTTP)
	authz.Register(m.registrar, api, op("template.eval-run-start", "POST", "/eval-runs", "eval-runs", "Freeze cases and request actual evaluation without inferred results"), manage, m.evalStartHTTP)
	authz.Register(m.registrar, api, op("template.eval-run-get", "GET", "/eval-runs/{run_id}", "eval-runs", "Read frozen evaluation provenance and actual verdicts"), read, m.evalGetHTTP)
}
func (m *Module) evalCreateHTTP(ctx context.Context, in *evalCaseCreateInput) (*respx.Body[EvalCaseView], error) {
	return command(m, ctx, in.IdempotencyKey, "template.eval-case-create", in.Body, func(tx *gorm.DB) (EvalCaseView, error) { return m.createEvalCaseOn(ctx, tx, in.Body) }, func(tx *gorm.DB) error {
		_, err := m.versionOn(ctx, tx, in.Body.TemplateVersionID, "template_manage")
		if err != nil {
			return err
		}
		return m.evalProjectOn(ctx, tx, in.Body.ProjectID, true)
	})
}
func (m *Module) evalPatchHTTP(ctx context.Context, in *evalCasePatchInput) (*respx.Body[EvalCaseView], error) {
	id, err := guid.RequireID(in.ID)
	version, e := bindingVersionHeader(in.IfMatch)
	if err != nil || e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	body := struct {
		Version int
		Patch   EvalCasePatch
	}{version, in.Body}
	return command(m, ctx, in.IdempotencyKey, "template.eval-case-patch:"+in.ID, body, func(tx *gorm.DB) (EvalCaseView, error) { return m.patchEvalCaseOn(ctx, tx, id, version, in.Body) }, func(tx *gorm.DB) error { _, err := m.evalCaseOn(ctx, tx, id, "template_manage"); return err })
}
func (m *Module) evalStartHTTP(ctx context.Context, in *evalStartInput) (*respx.Body[EvalRunView], error) {
	return command(m, ctx, in.IdempotencyKey, "template.eval-run-start", in.Body, func(tx *gorm.DB) (EvalRunView, error) { return m.startEvalOn(ctx, tx, in.Body) }, func(tx *gorm.DB) error {
		_, err := m.versionOn(ctx, tx, in.Body.TemplateVersionID, "template_manage")
		return err
	})
}
func (m *Module) evalGetHTTP(ctx context.Context, in *evalGetInput) (*respx.Body[EvalRunView], error) {
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	var view EvalRunView
	err = m.transaction(ctx, func(tx *gorm.DB) error { var e error; view, e = m.evalRunOn(ctx, tx, id); return e })
	if err != nil {
		return nil, templateError(ctx, err)
	}
	return respx.OK(ctx, view), nil
}
func (m *Module) evalListHTTP(ctx context.Context, in *evalListInput) (*respx.Body[EvalCasePage], error) {
	after, err := calibrationCursor(in.Cursor)
	if err != nil || in.Limit < 1 || in.Limit > 100 || in.Sort != "id" || in.Active != "" && in.Active != "true" && in.Active != "false" {
		return nil, templateError(ctx, ErrInvalid)
	}
	out := EvalCasePage{Items: []EvalCaseView{}}
	err = m.transaction(ctx, func(tx *gorm.DB) error {
		if e := m.authorizeOn(ctx, tx, "template_read", 0); e != nil {
			return e
		}
		query := tx.WithContext(ctx).Table(m.names.EvalCases.String()).Where("tenant_id=? AND id>?", authn.TenantIDFromContext(ctx), after)
		if in.TemplateKey != "" {
			query = query.Where("template_key=?", in.TemplateKey)
		}
		if in.Origin != "" {
			query = query.Where("origin=?", in.Origin)
		}
		if in.Active != "" {
			query = query.Where("active=?", in.Active == "true")
		}
		// Authorize before pagination so an inaccessible case neither leaks nor
		// consumes the visible page. Scan in bounded batches until the page fills.
		for {
			var rows []model.EvalCase
			if e := query.Order("id").Limit(100).Find(&rows).Error; e != nil {
				return e
			}
			for _, row := range rows {
				if _, e := m.versionOn(ctx, tx, row.TemplateVersionID, "template_read"); e != nil {
					if errors.Is(e, ErrForbidden) {
						continue
					}
					return e
				}
				if e := m.evalProjectOn(ctx, tx, row.ProjectID, false); e != nil {
					if errors.Is(e, ErrForbidden) {
						continue
					}
					return e
				}
				if len(out.Items) == in.Limit {
					out.HasMore = true
					out.NextCursor = out.Items[len(out.Items)-1].ID.String()
					return nil
				}
				view, e := evalCaseView(&row)
				if e != nil {
					return e
				}
				out.Items = append(out.Items, view)
			}
			if len(rows) < 100 {
				return nil
			}
			query = query.Where("id>?", rows[len(rows)-1].ID)
		}
	})
	if err != nil {
		return nil, templateError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) evalRunsHTTP(ctx context.Context, in *evalListInput) (*respx.Body[EvalRunPage], error) {
	after, err := calibrationCursor(in.Cursor)
	if err != nil || in.Limit < 1 || in.Limit > 100 || in.Sort != "id" {
		return nil, templateError(ctx, ErrInvalid)
	}
	out := EvalRunPage{Items: []EvalRunView{}}
	err = m.transaction(ctx, func(tx *gorm.DB) error {
		if e := m.authorizeOn(ctx, tx, "template_read", 0); e != nil {
			return e
		}
		query := tx.WithContext(ctx).Table(m.names.EvalRuns.String()).Where("tenant_id=? AND id>?", authn.TenantIDFromContext(ctx), after)
		if in.TemplateKey != "" {
			query = query.Where("template_key=?", in.TemplateKey)
		}
		for {
			var rows []model.EvalRun
			if e := query.Order("id").Limit(100).Find(&rows).Error; e != nil {
				return e
			}
			for _, row := range rows {
				view, e := m.evalRunOn(ctx, tx, row.ID)
				if e != nil {
					if errors.Is(e, ErrForbidden) {
						continue
					}
					return e
				}
				if len(out.Items) == in.Limit {
					out.HasMore = true
					out.NextCursor = out.Items[len(out.Items)-1].ID.String()
					return nil
				}
				out.Items = append(out.Items, view)
			}
			if len(rows) < 100 {
				return nil
			}
			query = query.Where("id>?", rows[len(rows)-1].ID)
		}
	})
	if err != nil {
		return nil, templateError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
