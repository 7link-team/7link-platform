package templates

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"strconv"
)

type studioListInput struct {
	ProjectID string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Domain    string `query:"domain" enum:"VIDEO,NOVEL,TEAM_TASKS" required:"true"`
	Cursor    string `query:"cursor" maxLength:"19"`
	Limit     int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Sort      string `query:"sort" enum:"id" default:"id"`
}
type studioCreateInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           StudioDraft
}
type studioDecisionInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID             string `path:"record_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           StudioDecision
}

func (m *Module) registerStudioHTTP(api huma.API) {
	m.registerTeamBoardHTTP(api)
	m.registerNovelHTTP(api)
	m.registerVideoHTTP(api)
	guard := authz.Guard{Resource: "template", Verb: "read", FilterData: true}
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"projects"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("template.studio-list", "GET", "/projects/{project_id}/studio-records", "List pinned domain studio records", 200), guard, m.studioListHTTP)
	authz.Register(m.registrar, api, op("template.studio-create", "POST", "/projects/{project_id}/studio-records", "Record domain source material without claiming execution facts", 201), guard, m.studioCreateHTTP)
	authz.Register(m.registrar, api, op("template.studio-decide", "POST", "/projects/{project_id}/studio-records/{record_id}/decisions", "Human candidate selection, draft confirmation or issue resolution", 200), guard, m.studioDecisionHTTP)
}
func (m *Module) studioListHTTP(ctx context.Context, in *studioListInput) (*respx.Body[StudioRecordPage], error) {
	project, err := guid.RequireID(in.ProjectID)
	if err != nil || in.Limit < 1 || in.Limit > 100 || in.Sort != "id" || (in.Domain != "VIDEO" && in.Domain != "NOVEL" && in.Domain != "TEAM_TASKS") {
		return nil, templateError(ctx, ErrInvalid)
	}
	var after int64
	if in.Cursor != "" {
		after, err = strconv.ParseInt(in.Cursor, 10, 64)
		if err != nil || after < 1 {
			return nil, templateError(ctx, ErrInvalid)
		}
	}
	out := StudioRecordPage{Items: []StudioRecordView{}}
	err = m.transaction(ctx, func(tx *gorm.DB) error {
		instance, e := m.instanceOn(ctx, tx, project)
		if e != nil {
			return e
		}
		q := query.BindStudioRecords(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.BindingID.Eq(instance.ID), q.Domain.Eq(in.Domain), q.ID.Gt(guid.ID(after))).Order(q.ID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
		}
		for _, row := range rows {
			view, e := studioView(row)
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
	if err != nil {
		return nil, templateError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) studioCreateHTTP(ctx context.Context, in *studioCreateInput) (*respx.Body[StudioRecordView], error) {
	project, err := guid.RequireID(in.ProjectID)
	if err != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return command(m, ctx, in.IdempotencyKey, "template.studio-create:"+in.ProjectID, in.Body, func(tx *gorm.DB) (StudioRecordView, error) { return m.createStudioOn(ctx, tx, project, in.Body) }, func(tx *gorm.DB) error { _, e := m.studioAuthorizeOn(ctx, tx, project); return e })
}
func (m *Module) studioDecisionHTTP(ctx context.Context, in *studioDecisionInput) (*respx.Body[StudioRecordView], error) {
	project, err := guid.RequireID(in.ProjectID)
	if err != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return command(m, ctx, in.IdempotencyKey, "template.studio-decide:"+in.ProjectID+":"+in.ID, in.Body, func(tx *gorm.DB) (StudioRecordView, error) { return m.decideStudioOn(ctx, tx, project, id, in.Body) }, func(tx *gorm.DB) error {
		binding, e := m.studioAuthorizeOn(ctx, tx, project)
		if e != nil {
			return e
		}
		_, e = m.studioRecordOn(ctx, tx, binding.ID, id)
		return e
	})
}
