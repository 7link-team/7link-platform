package templates

import (
	"context"
	"strconv"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type lifecycleListInput struct {
	ID     string `path:"template_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Cursor string `query:"cursor" maxLength:"19"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Sort   string `query:"sort" enum:"id" default:"id"`
}
type LifecyclePage struct {
	Items      []LifecycleView `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
	HasMore    bool            `json:"has_more"`
}
type providerStateInput struct {
	ID             string `path:"template_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Reference string  `json:"reference" minLength:"1" maxLength:"120"`
		ProjectID guid.ID `json:"project_id"`
		PriorID   guid.ID `json:"prior_id"`
		State     string  `json:"state" enum:"ENABLED,DISABLED"`
	}
}

func (m *Module) registerLifecycleHTTP(api huma.API) {
	manage := authz.Guard{Resource: "template", Verb: "manage", FilterData: true}
	authz.Register(m.registrar, api, huma.Operation{OperationID: "template.lifecycle-list", Method: "GET", Path: "/templates/{template_id}/lifecycle-receipts", Summary: "Read immutable calibration, provider and evaluation observations", Tags: []string{"templates"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, manage, m.lifecycleListHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "template.provider-state", Method: "POST", Path: "/templates/{template_id}/provider-state-changes", Summary: "Enable or disable a template provider using current authority", Tags: []string{"templates"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, manage, m.providerStateHTTP)
}
func (m *Module) lifecycleListHTTP(ctx context.Context, in *lifecycleListInput) (*respx.Body[LifecyclePage], error) {
	id, err := guid.RequireID(in.ID)
	if err != nil || in.Limit < 1 || in.Limit > 100 || in.Sort != "id" {
		return nil, templateError(ctx, ErrInvalid)
	}
	var after guid.ID
	if in.Cursor != "" {
		n, e := strconv.ParseInt(in.Cursor, 10, 64)
		if e != nil || n < 1 {
			return nil, templateError(ctx, ErrInvalid)
		}
		after = guid.ID(n)
	}
	out := LifecyclePage{Items: []LifecycleView{}}
	err = m.transaction(ctx, func(tx *gorm.DB) error {
		if _, e := m.versionOn(ctx, tx, id, "template_manage"); e != nil {
			return e
		}
		q := query.BindLifecycleReceipts(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.TemplateVersionID.Eq(id), q.ID.Gt(after)).Order(q.ID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
		}
		for _, r := range rows {
			if r.ProjectID > 0 {
				if m.scope.AuthorizeOn == nil {
					return ErrUnavailable
				}
				if e = m.scope.AuthorizeOn(ctx, tx, r.TenantID, r.ProjectID, "project_manage"); e != nil {
					return e
				}
			}
			v, e := lifecycleView(r)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, v)
		}
		if out.HasMore {
			out.NextCursor = rows[len(rows)-1].ID.String()
		}
		return nil
	})
	if err != nil {
		return nil, templateError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) providerStateHTTP(ctx context.Context, in *providerStateInput) (*respx.Body[LifecycleView], error) {
	id, err := guid.RequireID(in.ID)
	if err != nil || in.Body.State != "ENABLED" && in.Body.State != "DISABLED" {
		return nil, templateError(ctx, ErrInvalid)
	}
	observation := ProviderObservation{Source: ObservationSource{TemplateVersionID: id, ProjectID: in.Body.ProjectID, SourceKey: "provider-command:" + id.String() + ":" + authn.PrincipalIDFromContext(ctx).String() + ":" + in.IdempotencyKey, Reference: in.Body.Reference, PriorID: in.Body.PriorID}, State: in.Body.State}
	return command(m, ctx, in.IdempotencyKey, "template.provider-state:"+in.ID, in.Body, func(tx *gorm.DB) (LifecycleView, error) { return m.recordProviderOn(ctx, tx, observation) }, func(tx *gorm.DB) error {
		_, err := m.lifecycleAuthorityOn(ctx, tx, observation.Source)
		return err
	})
}
