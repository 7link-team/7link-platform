package clientwork

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"io"
	"mime"
	"strconv"
)

const (
	previewPurpose = "clientwork.preview"
	packagePurpose = "clientwork.package"
)

type packageFile struct {
	VersionID guid.ID `json:"version_id"`
	BindingID guid.ID `json:"binding_id"`
}

func (m *Module) previewResourceOn(ctx context.Context, tx *gorm.DB, id guid.ID, owner files.ResourceOwner) error {
	p := authn.EntityIDFromContext(ctx)
	if owner.Scope != "entity" || owner.ID != p {
		return ErrNotFound
	}
	ext, e := m.authorize(ctx, tx, p, "project_read", true)
	if e != nil {
		return e
	}
	_, e = m.previewOn(ctx, tx, p, id, ext)
	return e
}
func (m *Module) packageResourceOn(ctx context.Context, tx *gorm.DB, id guid.ID, owner files.ResourceOwner) error {
	if grant, ok := ctx.Value(submissionBundleGrantKey{}).(submissionBundleGrant); ok {
		return m.submissionBundleResourceOn(ctx, tx, id, owner, grant)
	}
	p := authn.EntityIDFromContext(ctx)
	if owner.Scope != "entity" || owner.ID != p {
		return ErrNotFound
	}
	ext, e := m.authorize(ctx, tx, p, "delivery_read", true)
	if e != nil {
		return e
	}
	_, e = m.packageOn(ctx, tx, p, id, ext)
	return e
}
func (m *Module) pinArtifactOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, purpose, kind string, ref delivery.ClientArtifactRef) (files.PinnedResource, error) {
	if m.files.OpenOn == nil {
		return files.PinnedResource{}, ErrUnavailable
	}
	v, e := m.projects.ArtifactFileOn(ctx, tx, authn.TenantIDFromContext(ctx), p, ref)
	if e != nil {
		return files.PinnedResource{}, e
	}
	bid, e := m.nextID()
	if e != nil {
		return files.PinnedResource{}, e
	}
	return m.files.PinOn(ctx, tx, files.ResourceBindingInput{ID: bid, SpaceID: v.SpaceID, FileID: v.FileID, ExpectedFileVersion: v.NodeVersion, VersionID: &v.VersionID, Purpose: purpose, ResourceKind: kind, ResourceID: id, Slot: "artifact." + v.VersionID.String(), ActorID: authn.PrincipalIDFromContext(ctx)})
}

type packageContentInput struct {
	EntityID  string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID        string `path:"id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	VersionID string `path:"version_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) registerContentHTTP(api huma.API) {
	owner := authz.ResourceOwnerResolver(m.owner)
	guard := authz.Guard{Resource: "channel", Verb: "read"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.preview.content", Method: "GET", Path: entityBase + "/artifact-previews/{id}/content", Summary: "Download the authorized fixed preview file", Tags: []string{"entities"}, Responses: binaryContentResponses(), Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.previewContentHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package.content", Method: "GET", Path: entityBase + "/delivery-packages/{id}/artifacts/{version_id}/content", Summary: "Download a fixed artifact from the published client package", Tags: []string{"entities"}, Responses: binaryContentResponses(), Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.packageContentHTTP)
}
func (m *Module) previewContentHTTP(ctx context.Context, in *ObjectInput) (*huma.StreamResponse, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.streamContent(ctx, p, func(tx *gorm.DB, ext bool) (guid.ID, string, error) {
		row, e := m.previewOn(ctx, tx, p, id, ext)
		if e != nil {
			return 0, "", e
		}
		if row.BindingID == nil {
			return 0, "", ErrNotFound
		}
		return *row.BindingID, row.ContentHash, nil
	})
}
func (m *Module) packageContentHTTP(ctx context.Context, in *packageContentInput) (*huma.StreamResponse, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	v, e := guid.RequireID(in.VersionID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.streamContent(ctx, p, func(tx *gorm.DB, ext bool) (guid.ID, string, error) {
		row, e := m.packageOn(ctx, tx, p, id, ext)
		if e != nil {
			return 0, "", e
		}
		var manifest packageManifest
		if e = json.Unmarshal([]byte(row.Manifest), &manifest); e != nil {
			return 0, "", e
		}
		for _, f := range manifest.Bindings {
			if f.VersionID == v {
				for _, a := range manifest.Content.Artifacts {
					if a.VersionID == v {
						return f.BindingID, a.SHA256, nil
					}
				}
			}
		}
		return 0, "", ErrNotFound
	})
}
func (m *Module) streamContent(ctx context.Context, p guid.ID, find func(*gorm.DB, bool) (guid.ID, string, error)) (*huma.StreamResponse, error) {
	return m.streamPinnedContent(ctx, p, false, find)
}
func (m *Module) streamPinnedContent(ctx context.Context, p guid.ID, inline bool, find func(*gorm.DB, bool) (guid.ID, string, error)) (*huma.StreamResponse, error) {
	if m.writer() == nil || m.files.OpenOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var download files.PinnedResourceRead
	e := m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, "project_read", true)
		if e != nil {
			return e
		}
		binding, hash, e := find(tx, ext)
		if e != nil {
			return e
		}
		download, e = m.files.OpenOn(ctx, tx, binding)
		if e != nil {
			return e
		}
		if download.SHA256 != hash {
			return ErrConflict
		}
		return nil
	})
	if e != nil {
		if download.Reader != nil {
			_ = download.Reader.Close()
		}
		return nil, httpError(ctx, e)
	}
	return pinnedContentResponse(download, inline), nil
}

func pinnedContentResponse(download files.PinnedResourceRead, inline bool) *huma.StreamResponse {
	return &huma.StreamResponse{Body: func(stream huma.Context) {
		defer func() { _ = download.Reader.Close() }()
		contentType := download.MIME
		if inline {
			switch contentType {
			case "image/png", "image/jpeg", "image/gif", "image/webp", "application/pdf", "text/plain":
			default:
				contentType = "text/plain"
			}
			stream.SetHeader("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
			stream.SetHeader("Referrer-Policy", "no-referrer")
		}
		stream.SetHeader("Content-Type", contentType)
		stream.SetHeader("Content-Length", strconv.FormatInt(download.Size, 10))
		disposition := "attachment"
		if inline {
			disposition = "inline"
		}
		stream.SetHeader("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": download.Name}))
		stream.SetHeader("X-Content-Type-Options", "nosniff")
		stream.SetHeader("Cache-Control", "private, no-store")
		_, _ = io.Copy(stream.BodyWriter(), download.Reader)
	}}
}

func binaryContentResponses() map[string]*huma.Response {
	return map[string]*huma.Response{"200": {Description: "Authorized fixed-version file bytes; Content-Type identifies the actual media type.", Content: map[string]*huma.MediaType{"*/*": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}}}}
}
