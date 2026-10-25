package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"io"
	"mime"
	"net/http"
	"strconv"
)

func (m *Module) injectAttachments(s *module.Services) error {
	var err error
	if m.fileResources, err = module.Resolve[files.ConversationResources](s); err != nil {
		return err
	}
	bindings, err := module.Resolve[*files.ResourceBindings](s)
	if err != nil {
		return err
	}
	return bindings.RegisterPurpose(files.ResourcePurpose{Name: "channel.attachment", ResourceKind: "message", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: m.authorizeAttachmentResourceOn})
}
func (m *Module) authorizeAttachmentResourceOn(ctx context.Context, tx *gorm.DB, message guid.ID, owner files.ResourceOwner) error {
	if owner.Scope != "entity" {
		return ErrForbidden
	}
	q := query.BindMessages(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.ID.Eq(message), q.TenantID.Eq(authn.TenantIDFromContext(ctx))).First()
	if err != nil {
		return err
	}
	_, err = m.conversionSourceOn(ctx, tx, r.TenantID, owner.ID, r.ChannelID, message)
	return err
}
func fileContext(ctx context.Context, project guid.ID) context.Context {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil {
		return ctx
	}
	copyClaims := *claims
	copyClaims.EntityID = project
	return authn.WithClaims(ctx, &copyClaims)
}
func (m *Module) pinAttachmentsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, row *model.Message, in []AttachmentInput) ([]AttachmentView, error) {
	out := []AttachmentView{}
	if len(in) > 0 && m.fileResources.PinOn == nil {
		return nil, ErrUnavailable
	}
	for i, a := range in {
		id, err := m.ids.Next()
		if err != nil {
			return nil, err
		}
		if id <= 0 {
			return nil, ErrUnavailable
		}
		pin, err := m.fileResources.PinOn(fileContext(ctx, project), tx, files.ResourceBindingInput{ID: guid.ID(id), SpaceID: a.SpaceID, FileID: a.FileID, ExpectedFileVersion: a.ExpectedFileVersion, VersionID: &a.VersionID, Purpose: "channel.attachment", ResourceKind: "message", ResourceID: row.ID, Slot: fmt.Sprintf("attachment.%d", i), ActorID: authn.PrincipalIDFromContext(ctx)})
		if err != nil {
			return nil, err
		}
		attachment := model.MessageAttachment{ID: pin.BindingID, TenantID: tenant, ChannelID: row.ChannelID, MessageID: row.ID, FileID: pin.FileID, VersionID: pin.VersionID}
		if err = query.BindAttachments(tx, m.names).WithContext(ctx).Create(&attachment); err != nil {
			return nil, err
		}
		out = append(out, AttachmentView{ID: attachment.ID, FileID: attachment.FileID, VersionID: attachment.VersionID})
	}
	return out, nil
}

type AttachmentPath struct {
	ConversationChannelInput
	MessageID    string `path:"message_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	AttachmentID string `path:"attachment_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type AttachmentCandidateInput struct {
	ConversationChannelInput
	FileID string `path:"file_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type AttachmentCandidate struct {
	AttachmentInput
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
}

func (m *Module) attachmentCandidateHTTP(ctx context.Context, in *AttachmentCandidateInput) (*respx.Body[AttachmentCandidate], error) {
	channel, e := guid.RequireID(in.ChannelID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	file, e := guid.RequireID(in.FileID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, e := m.projectForChannel(ctx, channel)
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	db, e := m.database()
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	var out AttachmentCandidate
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, _, err := m.authorizeChannel(ctx, tx, authn.TenantIDFromContext(ctx), project, channel, "channel_post"); err != nil {
			return err
		}
		if m.fileResources.InspectOn == nil {
			return ErrUnavailable
		}
		c, err := m.fileResources.InspectOn(fileContext(ctx, project), tx, file)
		if err != nil {
			return err
		}
		out = AttachmentCandidate{AttachmentInput: AttachmentInput{SpaceID: c.SpaceID, FileID: c.FileID, VersionID: c.VersionID, ExpectedFileVersion: c.NodeVersion}, Name: c.Name, MIME: c.MIME, Size: c.Size}
		return nil
	})
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) registerAttachmentsHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "channel.attachment_candidate", Method: http.MethodGet, Path: "/channels/{channel_id}/attachment-candidates/{file_id}", Summary: "Resolve an authorized file revision for a message attachment", Tags: []string{"channels"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, "channel_id", m.channelOwner, authz.Guard{Resource: "channel", Verb: "post"}, m.attachmentCandidateHTTP)

	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "channel.attachment_download", Method: http.MethodGet, Path: "/channels/{channel_id}/messages/{message_id}/attachments/{attachment_id}/content", Summary: "Download a visible message attachment at its pinned version", Tags: []string{"channels"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, "channel_id", m.channelOwner, authz.Guard{Resource: "channel", Verb: "read"}, m.attachmentHTTP)
}
func (m *Module) attachmentHTTP(ctx context.Context, in *AttachmentPath) (*huma.StreamResponse, error) {
	channel, e := guid.RequireID(in.ChannelID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	message, e := guid.RequireID(in.MessageID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	attachment, e := guid.RequireID(in.AttachmentID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, e := m.projectForChannel(ctx, channel)
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	db, e := m.database()
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	var result files.PinnedResourceRead
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if _, e := m.conversionSourceOn(ctx, tx, tenant, project, channel, message); e != nil {
			return e
		}
		q := query.BindAttachments(tx, m.names)
		_, e := q.WithContext(ctx).Where(q.ID.Eq(attachment), q.TenantID.Eq(tenant), q.ChannelID.Eq(channel), q.MessageID.Eq(message)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if m.fileResources.OpenOn == nil {
			return ErrUnavailable
		}
		result, e = m.fileResources.OpenOn(fileContext(ctx, project), tx, attachment, message)
		return e
	})
	if e != nil {
		if result.Reader != nil {
			_ = result.Reader.Close()
		}
		return nil, conversationHTTPError(ctx, e)
	}
	return &huma.StreamResponse{Body: func(stream huma.Context) {
		defer func() { _ = result.Reader.Close() }()
		stream.SetHeader("Content-Type", result.MIME)
		stream.SetHeader("Content-Length", strconv.FormatInt(result.Size, 10))
		stream.SetHeader("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": result.Name}))
		stream.SetHeader("X-Content-Type-Options", "nosniff")
		_, _ = io.Copy(stream.BodyWriter(), result.Reader)
	}}, nil
}
