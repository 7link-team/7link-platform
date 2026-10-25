package templates

import (
	"context"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type novelInput struct {
	ProjectID string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type novelCreateInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           NovelEdit
}
type novelEditInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID             string `path:"record_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           NovelEdit
}

func (m *Module) registerNovelHTTP(api huma.API) {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"projects"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	guard := authz.Guard{Resource: "template", Verb: "read", FilterData: true}
	authz.Register(m.registrar, api, op("template.novel-workbench", "GET", "/projects/{project_id}/novel-workbench", "Read the whole recorded manuscript and pinned continuity evidence", 200), guard, m.novelReadHTTP)
	authz.Register(m.registrar, api, op("template.novel-create", "POST", "/projects/{project_id}/novel-workbench", "Save structured novel material or run whole-book deterministic checks", 201), guard, m.novelCreateHTTP)
	authz.Register(m.registrar, api, op("template.novel-edit", "POST", "/projects/{project_id}/novel-workbench/{record_id}/decisions", "Save a versioned bible, chapter or foreshadow ledger entry", 200), guard, m.novelEditHTTP)
}
func (m *Module) novelReadHTTP(ctx context.Context, in *novelInput) (*respx.Body[NovelWorkbench], error) {
	project, e := guid.RequireID(in.ProjectID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	var out NovelWorkbench
	e = m.transaction(ctx, func(tx *gorm.DB) error { var err error; out, err = m.novelWorkbenchOn(ctx, tx, project); return err })
	if e != nil {
		return nil, templateError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) novelCreateHTTP(ctx context.Context, in *novelCreateInput) (*respx.Body[StudioRecordView], error) {
	project, e := guid.RequireID(in.ProjectID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return m.novelCommandHTTP(ctx, project, 0, in.IdempotencyKey, in.Body)
}
func (m *Module) novelEditHTTP(ctx context.Context, in *novelEditInput) (*respx.Body[StudioRecordView], error) {
	project, e := guid.RequireID(in.ProjectID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil || in.Body.Action != "SAVE" {
		return nil, templateError(ctx, ErrInvalid)
	}
	return m.novelCommandHTTP(ctx, project, id, in.IdempotencyKey, in.Body)
}
func (m *Module) novelCommandHTTP(ctx context.Context, project, id guid.ID, key string, in NovelEdit) (*respx.Body[StudioRecordView], error) {
	return command(m, ctx, key, "template.novel:"+project.String()+":"+id.String(), in, func(tx *gorm.DB) (StudioRecordView, error) { return m.novelEditOn(ctx, tx, project, id, in) }, func(tx *gorm.DB) error {
		b, e := m.studioAuthorizeOn(ctx, tx, project)
		if e != nil {
			return e
		}
		if id > 0 {
			_, e = m.studioRecordOn(ctx, tx, b.ID, id)
		}
		return e
	})
}
