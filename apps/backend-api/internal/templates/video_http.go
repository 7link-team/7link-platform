package templates

import (
	"context"
	"io"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type videoCreateInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           VideoEdit
}
type videoEditInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID             string `path:"record_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           VideoEdit
}
type videoContentInput struct {
	ProjectID string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID        string `path:"record_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) registerVideoHTTP(api huma.API) {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"projects"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	guard := authz.Guard{Resource: "template", Verb: "read", FilterData: true}
	authz.Register(m.registrar, api, op("template.video-workbench", "GET", "/projects/{project_id}/video-workbench", "Read shots, provider jobs, pinned media and actual execution QC receipts", 200), guard, m.videoReadHTTP)
	authz.Register(m.registrar, api, op("template.video-create", "POST", "/projects/{project_id}/video-workbench", "Record shots, request generation or import hash-matching provider output", 201), guard, m.videoCreateHTTP)
	authz.Register(m.registrar, api, op("template.video-edit", "POST", "/projects/{project_id}/video-workbench/{record_id}/decisions", "Edit a versioned shot or select its candidate", 200), guard, m.videoEditHTTP)
	authz.Register(m.registrar, api, op("template.video-content", "GET", "/projects/{project_id}/video-workbench/{record_id}/content", "Stream an authorized immutable media artifact", 200), guard, m.videoContentHTTP)
}
func (m *Module) videoReadHTTP(ctx context.Context, in *novelInput) (*respx.Body[VideoWorkbench], error) {
	project, e := guid.RequireID(in.ProjectID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	var out VideoWorkbench
	e = m.transaction(ctx, func(tx *gorm.DB) error { var err error; out, err = m.videoWorkbenchOn(ctx, tx, project); return err })
	if e != nil {
		return nil, templateError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) videoCreateHTTP(ctx context.Context, in *videoCreateInput) (*respx.Body[StudioRecordView], error) {
	project, e := guid.RequireID(in.ProjectID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return m.videoCommandHTTP(ctx, project, 0, in.IdempotencyKey, in.Body)
}
func (m *Module) videoEditHTTP(ctx context.Context, in *videoEditInput) (*respx.Body[StudioRecordView], error) {
	project, e := guid.RequireID(in.ProjectID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return m.videoCommandHTTP(ctx, project, id, in.IdempotencyKey, in.Body)
}
func (m *Module) videoCommandHTTP(ctx context.Context, project, id guid.ID, key string, in VideoEdit) (*respx.Body[StudioRecordView], error) {
	return command(m, ctx, key, "template.video:"+project.String()+":"+id.String(), in, func(tx *gorm.DB) (StudioRecordView, error) { return m.videoEditOn(ctx, tx, project, id, in) }, func(tx *gorm.DB) error {
		if in.Content.Video != nil && in.Content.Video.ExecutionID > 0 {
			if _, e := m.videoSourceOn(ctx, tx, project, in.Content.Video.ExecutionID); e != nil {
				return e
			}
		}
		if in.Action == "GENERATE" {
			if in.Content.Video == nil || m.providerExecution == nil {
				return ErrUnavailable
			}
			if e := m.providerExecution(ctx, tx, authn.TenantIDFromContext(ctx), project, in.Content.Video.ExecutionID, "GENERATE"); e != nil {
				return e
			}
		}
		b, e := m.studioAuthorizeOn(ctx, tx, project)
		if e != nil {
			return e
		}
		if id > 0 {
			row, err := m.studioRecordOn(ctx, tx, b.ID, id)
			if err != nil {
				return err
			}
			view, err := studioView(row)
			if err != nil {
				return err
			}
			if view.Content.Video != nil && view.Content.Video.ExecutionID > 0 {
				_, e = m.videoSourceOn(ctx, tx, project, view.Content.Video.ExecutionID)
			}
		}
		return e
	})
}
func (m *Module) videoContentHTTP(ctx context.Context, in *videoContentInput) (*huma.StreamResponse, error) {
	project, e := guid.RequireID(in.ProjectID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	var resource files.PinnedResourceRead
	e = m.transaction(ctx, func(tx *gorm.DB) error {
		var err error
		resource, err = m.videoOpenOn(ctx, tx, project, id)
		return err
	})
	if e != nil {
		if resource.Reader != nil {
			_ = resource.Reader.Close()
		}
		return nil, templateError(ctx, e)
	}
	return &huma.StreamResponse{Body: func(ctx huma.Context) {
		defer func() { _ = resource.Reader.Close() }()
		ctx.SetHeader("Content-Type", resource.MIME)
		ctx.SetHeader("Cache-Control", "private, no-store")
		ctx.SetHeader("X-Content-Type-Options", "nosniff")
		ctx.SetHeader("Content-Security-Policy", "sandbox; default-src 'none'; img-src data: blob:; style-src 'unsafe-inline'")
		ctx.SetHeader("ETag", `"`+resource.SHA256+`"`)
		ctx.SetStatus(200)
		_, _ = io.Copy(ctx.BodyWriter(), resource.Reader)
	}}, nil
}
