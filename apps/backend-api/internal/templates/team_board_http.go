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

type teamBoardInput struct {
	ProjectID string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Cursor    string `query:"cursor" maxLength:"19"`
	Limit     int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Sort      string `query:"sort" enum:"id" default:"id"`
}
type TeamBoardPage struct {
	Items      []TeamTaskView     `json:"items"`
	Drafts     []StudioRecordView `json:"drafts"`
	NextCursor string             `json:"next_cursor,omitempty"`
	HasMore    bool               `json:"has_more"`
}
type teamEditInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID             string `path:"record_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           TeamEdit
}
type teamCreateInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           TeamEdit
}

func (m *Module) registerTeamBoardHTTP(api huma.API) {
	guard := authz.Guard{Resource: "template", Verb: "read", FilterData: true}
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"projects"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("template.team-board", "GET", "/projects/{project_id}/team-task-board", "Read human team checklist, risks and current delivery work", 200), guard, m.teamBoardHTTP)
	authz.Register(m.registrar, api, op("template.team-task-create", "POST", "/projects/{project_id}/team-task-board", "Link canonical human work to a structured task", 201), guard, m.teamCreateHTTP)
	authz.Register(m.registrar, api, op("template.team-task-edit", "POST", "/projects/{project_id}/team-task-board/{record_id}/decisions", "Save task evidence or record independent human sign-off", 200), guard, m.teamEditHTTP)
}
func (m *Module) teamBoardHTTP(ctx context.Context, in *teamBoardInput) (*respx.Body[TeamBoardPage], error) {
	project, err := guid.RequireID(in.ProjectID)
	if err != nil || in.Limit < 1 || in.Limit > 100 || in.Sort != "id" {
		return nil, templateError(ctx, ErrInvalid)
	}
	var after guid.ID
	if in.Cursor != "" {
		after, err = guid.RequireID(in.Cursor)
		if err != nil {
			return nil, templateError(ctx, ErrInvalid)
		}
	}
	out := TeamBoardPage{Items: []TeamTaskView{}, Drafts: []StudioRecordView{}}
	err = m.transaction(ctx, func(tx *gorm.DB) error {
		instance, e := m.instanceOn(ctx, tx, project)
		if e != nil {
			return e
		}
		if _, e = m.teamWorkFactOn(ctx, tx, project, 0); e != nil {
			return e
		}
		q := query.BindStudioRecords(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.BindingID.Eq(instance.ID), q.Domain.Eq("TEAM_TASKS"), q.ID.Gt(after)).Order(q.ID).Limit(in.Limit + 1).Find()
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
			if row.Kind == "ASSISTANT_DRAFT" || view.Content.Team == nil {
				out.Drafts = append(out.Drafts, view)
				continue
			}
			work, e := m.teamWorkFactOn(ctx, tx, project, view.Content.Team.WorkItemID)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, teamTaskView(view, work))
		}
		if out.HasMore {
			out.NextCursor = strconv.FormatInt(int64(rows[len(rows)-1].ID), 10)
		}
		return nil
	})
	if err != nil {
		return nil, templateError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) teamCommandHTTP(ctx context.Context, project, id guid.ID, key string, in TeamEdit) (*respx.Body[TeamTaskView], error) {
	return command(m, ctx, key, "template.team-task:"+project.String()+":"+id.String(), in, func(tx *gorm.DB) (TeamTaskView, error) { return m.teamEditOn(ctx, tx, project, id, in) }, func(tx *gorm.DB) error {
		if e := m.lockTeamParticipantsOn(ctx, tx, project, id, in.Content); e != nil {
			return e
		}
		binding, e := m.studioAuthorizeOn(ctx, tx, project)
		if e != nil {
			return e
		}
		if id > 0 {
			row, e := m.studioRecordOn(ctx, tx, binding.ID, id)
			if e != nil {
				return e
			}
			view, e := studioView(row)
			if e != nil {
				return e
			}
			if view.Content.Team != nil {
				_, e = m.teamWorkFactOn(ctx, tx, project, view.Content.Team.WorkItemID)
				return e
			}
		}
		_, e = m.teamWorkFactOn(ctx, tx, project, 0)
		if e == nil && in.Content.Team != nil {
			_, e = m.teamWorkFactOn(ctx, tx, project, in.Content.Team.WorkItemID)
		}
		return e
	})
}
func (m *Module) teamCreateHTTP(ctx context.Context, in *teamCreateInput) (*respx.Body[TeamTaskView], error) {
	project, err := guid.RequireID(in.ProjectID)
	if err != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return m.teamCommandHTTP(ctx, project, 0, in.IdempotencyKey, in.Body)
}
func (m *Module) teamEditHTTP(ctx context.Context, in *teamEditInput) (*respx.Body[TeamTaskView], error) {
	project, err := guid.RequireID(in.ProjectID)
	if err != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return m.teamCommandHTTP(ctx, project, id, in.IdempotencyKey, in.Body)
}
