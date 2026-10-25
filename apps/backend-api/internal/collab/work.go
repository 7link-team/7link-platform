package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"net/http"
	"slices"
)

var ErrWorkReadOnly = errors.New("collab: work project is read only")

type WorkCriterion struct {
	Text               string `json:"text" minLength:"1" maxLength:"2000"`
	VerificationMethod string `json:"verification_method" minLength:"1" maxLength:"2000"`
}
type ConvertWorkInput struct {
	Title                  string          `json:"title" minLength:"1" maxLength:"200"`
	Objective              string          `json:"objective" minLength:"1" maxLength:"2000"`
	AccountablePrincipalID guid.ID         `json:"accountable_principal_id" required:"true"`
	Criteria               []WorkCriterion `json:"criteria" minItems:"1" maxItems:"50"`
}
type WorkReceipt struct {
	WorkItemID guid.ID `json:"work_item_id"`
	MessageID  guid.ID `json:"message_id"`
	ProjectID  guid.ID `json:"project_id"`
}

// MessageWorkCreator executes inside the caller's idempotency savepoint.
// Delivery retains principal/project locks, current IAM, audit and outbox.
type MessageWorkCreator func(context.Context, *gorm.DB, guid.ID, guid.ID, ConvertWorkInput, string) (guid.ID, error)
type MessageWorkReplay func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
type MessageWorkConnector struct {
	Bind func(MessageWorkCreator, MessageWorkReplay) error
}

func (m *Module) bindWorkCreator(c MessageWorkCreator, replay MessageWorkReplay) error {
	if c == nil || replay == nil || m.workCreator != nil {
		return ErrInvalidArgument
	}
	m.workCreator = c
	m.workReplay = replay
	return nil
}
func (m *Module) provideWorkConnector(s *module.Services) error {
	return module.Provide[MessageWorkConnector](s, MessageWorkConnector{Bind: m.bindWorkCreator})
}
func (m *Module) conversionSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, message guid.ID) (*model.Message, error) {
	_, member, err := m.authorizeChannel(ctx, tx, tenant, project, channel, "channel_read")
	if err != nil {
		return nil, err
	}
	q := query.BindMessages(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(message), q.TenantID.Eq(tenant), q.ChannelID.Eq(channel)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && member.External && row.Visibility == "INTERNAL") {
		return nil, ErrNotFound
	}
	return row, err
}
func (m *Module) convertWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, message guid.ID, in ConvertWorkInput) (WorkReceipt, error) {
	out := WorkReceipt{MessageID: message, ProjectID: project}
	if m.principals.LockActiveRow == nil {
		return out, ErrUnavailable
	}
	ids := []guid.ID{authn.PrincipalIDFromContext(ctx), in.AccountablePrincipalID}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if id <= 0 {
			return out, ErrInvalidArgument
		}
		_, active, e := m.principals.LockActiveRow(ctx, tx, id)
		if e != nil {
			return out, e
		}
		if !active {
			return out, ErrForbidden
		}
	}
	source, err := m.conversionSourceOn(ctx, tx, tenant, project, channel, message)
	if err != nil {
		return out, err
	}
	if m.workCreator == nil {
		return out, ErrUnavailable
	}
	id, err := m.workCreator(ctx, tx, tenant, project, in, source.Body)
	if err != nil {
		return out, err
	}
	row := model.MessageWork{TenantID: tenant, MessageID: message, ChannelID: channel, ProjectID: project, WorkItemID: id, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: m.now()}
	if err = query.BindMessageWorks(tx, m.names).WithContext(ctx).Create(&row); err != nil {
		return out, err
	}
	out.WorkItemID = id
	err = m.appendAudit(ctx, tx, tenant, project, channel, "collab.message_converted", 0, ProvisioningAudit{AuthoritySource: "IAM:work_item_write"})
	return out, err
}

type convertWorkHTTPInput struct {
	ConversationChannelInput
	MessageID      string `path:"message_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           ConvertWorkInput
}

func (m *Module) convertWorkHTTP(ctx context.Context, in *convertWorkHTTPInput) (*commandOutput[WorkReceipt], error) {
	channel, err := guid.RequireID(in.ChannelID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	message, err := guid.RequireID(in.MessageID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, err := m.projectForChannel(ctx, channel)
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	tenant := authn.TenantIDFromContext(ctx)
	return conversationCommand(ctx, m, in.IdempotencyKey, "channel.message_convert", "/channels/{channel_id}/messages/{message_id}/work-items", map[string]string{"channel_id": in.ChannelID, "message_id": in.MessageID}, in.Body, http.StatusCreated, func(tx *gorm.DB) (WorkReceipt, error) {
		return m.convertWorkOn(ctx, tx, tenant, project, channel, message, in.Body)
	}, func(tx *gorm.DB, r WorkReceipt) error {
		_, err := m.conversionSourceOn(ctx, tx, tenant, project, channel, message)
		if err != nil {
			return err
		}
		if m.workReplay == nil {
			return ErrUnavailable
		}
		return m.workReplay(ctx, tx, tenant, project, r.WorkItemID)
	})
}
