package performance

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	pi "7link.com/platform/internal/performance/i18n"
	"7link.com/platform/internal/performance/query"
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

type schemeBody struct {
	BaseVersion int              `json:"base_version" minimum:"0"`
	Definition  SchemeDefinition `json:"definition"`
	Reason      string           `json:"reason" minLength:"1" maxLength:"2000"`
}
type createInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           schemeBody
}
type reviseInput struct {
	SchemeID       string `path:"scheme_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           schemeBody
}
type getInput struct {
	SchemeID string `path:"scheme_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Version  int    `query:"version" minimum:"1" doc:"An explicit immutable revision number; omit for current."`
}
type listInput struct {
	Cursor string `query:"cursor" maxLength:"19"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
}
type historyInput struct {
	SchemeID string `path:"scheme_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Cursor   int    `query:"cursor" minimum:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
}
type RevisionPage struct {
	Items      []RevisionView `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m.registrar == nil {
		return ErrUnavailable
	}
	guard := authz.Guard{Resource: "performance", Verb: "manage"}
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("performance.scheme.list", "GET", "/performance-schemes", "List current owner assessment scheme revisions", 200), guard, m.listHTTP)
	authz.Register(m.registrar, api, op("performance.scheme.create", "POST", "/performance-schemes", "Create an immutable owner assessment scheme revision", 201), guard, m.createHTTP)
	authz.Register(m.registrar, api, op("performance.scheme.get", "GET", "/performance-schemes/{scheme_id}", "Read a current or historical assessment scheme revision", 200), guard, m.getHTTP)
	authz.Register(m.registrar, api, op("performance.scheme.history", "GET", "/performance-schemes/{scheme_id}/revisions", "List immutable assessment rule and template history", 200), guard, m.historyHTTP)
	authz.Register(m.registrar, api, op("performance.scheme.revise", "POST", "/performance-schemes/{scheme_id}/revisions", "Revise assessment rules, applicability, enablement and report template", 201), guard, m.reviseHTTP)
	m.registerReportHTTP(api)
	m.registerScheduleHTTP(api)
	return nil
}
func failure(ctx context.Context, e error) error {
	switch {
	case errors.Is(e, ErrInvalid), errors.Is(e, idempotency.ErrInvalidKey):
		return respx.ErrStatus(ctx, 422, 186422, pi.Invalid, nil)
	case errors.Is(e, ErrForbidden):
		return respx.ErrStatus(ctx, 403, 186403, pi.Forbidden, nil)
	case errors.Is(e, ErrNotFound):
		return respx.ErrStatus(ctx, 404, 186404, pi.NotFound, nil)
	case errors.Is(e, ErrConflict), errors.Is(e, idempotency.ErrKeyReused):
		return respx.ErrStatus(ctx, 409, 186410, pi.Conflict, nil)
	case errors.Is(e, ErrUnavailable):
		return respx.ErrStatus(ctx, 503, 186503, pi.Unavailable, nil)
	default:
		return respx.ErrStatus(ctx, 500, 186500, pi.Failed, nil)
	}
}
func (m *Module) transaction(ctx context.Context, fn func(*gorm.DB) error) error {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return ErrUnavailable
	}
	return m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.authorizeOn(ctx, tx); e != nil {
			return e
		}
		return fn(tx)
	})
}
func (m *Module) command(ctx context.Context, id guid.ID, key string, body schemeBody) (*respx.Body[RevisionView], error) {
	var out RevisionView
	raw, e := json.Marshal(struct {
		ID   guid.ID
		Body schemeBody
	}{id, body})
	if e != nil {
		return nil, failure(ctx, e)
	}
	e = m.transaction(ctx, func(tx *gorm.DB) error {
		if m.idem == nil {
			return ErrUnavailable
		}
		result, _, e := m.idem.Do(ctx, tx, idempotency.Key{TenantID: authn.TenantIDFromContext(ctx), SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "performance.scheme.revise:" + id.String(), Value: key, RequestHash: sha256.Sum256(raw)}, func(work *gorm.DB) (idempotency.Result, error) {
			view, e := m.reviseOn(ctx, work, id, body.BaseVersion, body.Definition, body.Reason)
			if e != nil {
				return idempotency.Result{}, e
			}
			b, e := json.Marshal(view)
			return idempotency.Result{StatusCode: 201, Body: b}, e
		})
		if e != nil {
			return e
		}
		return json.Unmarshal(result.Body, &out)
	})
	if e != nil {
		return nil, failure(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) createHTTP(ctx context.Context, in *createInput) (*respx.Body[RevisionView], error) {
	return m.command(ctx, 0, in.IdempotencyKey, in.Body)
}
func (m *Module) reviseHTTP(ctx context.Context, in *reviseInput) (*respx.Body[RevisionView], error) {
	id, e := guid.RequireID(in.SchemeID)
	if e != nil || in.Body.BaseVersion < 1 {
		return nil, failure(ctx, ErrInvalid)
	}
	return m.command(ctx, id, in.IdempotencyKey, in.Body)
}
func (m *Module) getHTTP(ctx context.Context, in *getInput) (*respx.Body[RevisionView], error) {
	id, e := guid.RequireID(in.SchemeID)
	if e != nil || in.Version < 0 {
		return nil, failure(ctx, ErrInvalid)
	}
	var out RevisionView
	e = m.transaction(ctx, func(tx *gorm.DB) error {
		head, e := m.schemeOn(ctx, tx, id)
		if e != nil {
			return e
		}
		version := in.Version
		if version == 0 {
			version = head.Version
		}
		q := query.BindRevisions(tx, m.names)
		row, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.SchemeID.Eq(id), q.Version.Eq(version)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		out, e = revisionView(row)
		return e
	})
	if e != nil {
		return nil, failure(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) listHTTP(ctx context.Context, in *listInput) (*respx.Body[RevisionPage], error) {
	after := guid.ID(0)
	if in.Cursor != "" {
		id, e := guid.RequireID(in.Cursor)
		if e != nil {
			return nil, failure(ctx, ErrInvalid)
		}
		after = id
	}
	if in.Limit < 1 || in.Limit > 100 {
		return nil, failure(ctx, ErrInvalid)
	}
	out := RevisionPage{Items: []RevisionView{}}
	e := m.transaction(ctx, func(tx *gorm.DB) error {
		q := query.BindSchemes(tx, m.names)
		r := query.BindRevisions(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Gt(after)).Order(q.ID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		out.HasMore = len(rows) > in.Limit
		if out.HasMore {
			rows = rows[:in.Limit]
		}
		for _, head := range rows {
			row, e := r.WithContext(ctx).Where(r.TenantID.Eq(head.TenantID), r.SchemeID.Eq(head.ID), r.Version.Eq(head.Version)).First()
			if e != nil {
				return e
			}
			view, e := revisionView(row)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, view)
		}
		if out.HasMore {
			out.NextCursor = rows[len(rows)-1].ID.String()
		}
		return nil
	})
	if e != nil {
		return nil, failure(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) historyHTTP(ctx context.Context, in *historyInput) (*respx.Body[RevisionPage], error) {
	id, e := guid.RequireID(in.SchemeID)
	if e != nil || in.Cursor < 0 || in.Limit < 1 || in.Limit > 100 {
		return nil, failure(ctx, ErrInvalid)
	}
	out := RevisionPage{Items: []RevisionView{}}
	e = m.transaction(ctx, func(tx *gorm.DB) error {
		if _, e := m.schemeOn(ctx, tx, id); e != nil {
			return e
		}
		q := query.BindRevisions(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.SchemeID.Eq(id), q.Version.Gt(in.Cursor)).Order(q.Version).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		out.HasMore = len(rows) > in.Limit
		if out.HasMore {
			rows = rows[:in.Limit]
		}
		for _, row := range rows {
			view, e := revisionView(row)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, view)
		}
		if out.HasMore {
			out.NextCursor = strconv.Itoa(rows[len(rows)-1].Version)
		}
		return nil
	})
	if e != nil {
		return nil, failure(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
