package collab

import (
	"context"
	"strconv"
	"strings"

	collabi18n "7link.com/platform/internal/collab/i18n"
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type MessagePathInput struct {
	MessageID string `path:"message_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type messageEditHTTPInput struct {
	MessagePathInput
	IfMatch        string `header:"If-Match"`
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"64"`
	Body           EditMessageInput
}
type messageRetractHTTPInput struct {
	MessagePathInput
	IfMatch        string `header:"If-Match"`
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"64"`
}
type messageRetractHTTPOutput struct {
	Replayed string `header:"Idempotent-Replayed"`
}

func messageExpectedVersion(ctx context.Context, raw string) (int, error) {
	if raw == "" {
		return 0, respx.ErrStatus(ctx, 428, 145428, collabi18n.Conflict, nil)
	}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "W/") {
		return 0, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	n, err := strconv.Atoi(strings.Trim(raw, `"`))
	if err != nil || n < 1 {
		return 0, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	return n, nil
}
func (m *Module) registerMessageMutationsHTTP(api huma.API) {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{401, 403, 404, 409, 422, 428, 500, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, op("message.edit", "PATCH", "/messages/{message_id}", "Edit your text within fifteen minutes", 200), m.editMessageHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("message.retract", "DELETE", "/messages/{message_id}", "Retract a text while retaining its audit revision", 204), m.retractMessageHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("message.revisions", "GET", "/messages/{message_id}/revisions", "Read text revisions with current audit authority", 200), m.revisionsMessageHTTP)
}
func (m *Module) messageMutationHTTP(ctx context.Context, message, key, etag string, in EditMessageInput, retract bool) (*commandOutput[MessageView], error) {
	id, err := guid.RequireID(message)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	expected, err := messageExpectedVersion(ctx, etag)
	if err != nil {
		return nil, err
	}
	operation := "message.edit"
	if retract {
		operation = "message.retract"
	}
	tenant := authn.TenantIDFromContext(ctx)
	body := struct {
		Version int              `json:"version"`
		Edit    EditMessageInput `json:"edit"`
	}{expected, in}
	return conversationCommand(ctx, m, key, operation, "/messages/{message_id}", map[string]string{"message_id": message}, body, 200, func(tx *gorm.DB) (MessageView, error) {
		return m.mutateMessageOn(ctx, tx, tenant, id, expected, in, retract)
	}, func(tx *gorm.DB, _ MessageView) error {
		permission := "channel_post"
		if retract {
			permission = "channel_read"
		}
		row, ch, _, e := m.messageAccessOn(ctx, tx, tenant, id, permission)
		if e != nil {
			return e
		}
		if retract && row.AuthorID != authn.PrincipalIDFromContext(ctx) {
			if ch.Kind == "DIRECT" || ch.ProjectID == nil {
				return ErrForbidden
			}
			_, _, e = m.authorizeChannel(ctx, tx, tenant, *ch.ProjectID, ch.ID, "channel_manage")
		}
		return e
	})
}
func (m *Module) editMessageHTTP(ctx context.Context, in *messageEditHTTPInput) (*commandOutput[MessageView], error) {
	return m.messageMutationHTTP(ctx, in.MessageID, in.IdempotencyKey, in.IfMatch, in.Body, false)
}
func (m *Module) retractMessageHTTP(ctx context.Context, in *messageRetractHTTPInput) (*messageRetractHTTPOutput, error) {
	out, err := m.messageMutationHTTP(ctx, in.MessageID, in.IdempotencyKey, in.IfMatch, EditMessageInput{}, true)
	if err != nil {
		return nil, err
	}
	return &messageRetractHTTPOutput{Replayed: out.Replayed}, nil
}
func (m *Module) revisionsMessageHTTP(ctx context.Context, in *MessagePathInput) (*respx.Body[[]model.MessageRevision], error) {
	id, err := guid.RequireID(in.MessageID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.database()
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	var rows []model.MessageRevision
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		rows, e = m.messageRevisionsOn(ctx, tx, authn.TenantIDFromContext(ctx), id)
		return e
	})
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	return respx.OK(ctx, rows), nil
}
