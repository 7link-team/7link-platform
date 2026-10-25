package collab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	collabi18n "7link.com/platform/internal/collab/i18n"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type ConversationEntityInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type ConversationChannelInput struct {
	ChannelID string `path:"channel_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type channelListInput struct {
	ConversationEntityInput
	Archived bool   `query:"archived" default:"false"`
	Cursor   string `query:"cursor" maxLength:"512"`
	Limit    int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type channelCreateInput struct {
	ConversationEntityInput
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"64"`
	Body           CreateChannelInput
}
type messageListInput struct {
	ConversationChannelInput
	Cursor   string `query:"cursor" maxLength:"512"`
	Limit    int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	AfterSeq int64  `query:"after_seq" minimum:"0"`
	ThreadID string `query:"reply_to_message_id"`
	Activity string `query:"activity" default:"all" enum:"all,unread,mentions,saved"`
	Search   string `query:"search" maxLength:"200"`
}
type messagePostInput struct {
	ConversationChannelInput
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"64"`
	Body           PostMessageInput
}
type memberPutInput struct {
	ConversationChannelInput
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body        MemberInput
}

type channelUpdateHTTPInput struct {
	ConversationChannelInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           ChannelUpdateInput
}

type channelDetailOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[ChannelView]
}
type readInput struct {
	ConversationChannelInput
	Body struct {
		Seq int64 `json:"seq" minimum:"0"`
	}
}
type saveInput struct {
	ConversationChannelInput
	MessageID string `path:"message_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body      struct {
		Saved bool `json:"saved"`
	}
}
type memberListInput struct {
	ConversationChannelInput
	Cursor string `query:"cursor" maxLength:"512"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type commandOutput[T any] struct {
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[T]
}

type channelCreateOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[ChannelView]
}

func (m *Module) database() (*gorm.DB, error) {
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, ErrUnavailable
	}
	return m.runtime.Database().Write(), nil
}
func (m *Module) channelOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, err := m.database()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	q := query.BindChannels(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	if row.ProjectID == nil {
		return authz.ResourceOwner{}, false, nil
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: row.TenantID, EntityID: *row.ProjectID}, true, nil
}
func (m *Module) projectForChannel(ctx context.Context, id guid.ID) (guid.ID, error) {
	owner, found, err := m.channelOwner(ctx, id)
	if err != nil {
		return 0, err
	}
	if !found || owner.TenantID != authn.TenantIDFromContext(ctx) {
		return 0, ErrNotFound
	}
	return owner.EntityID, nil
}
func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m.registrar == nil || m.owner == nil {
		return ErrUnavailable
	}
	m.registerAttachmentsHTTP(api)
	m.registerMessageMutationsHTTP(api)
	if err := m.registerConversationsHTTP(api); err != nil {
		return err
	}
	// Permission declaration must not register the same locale bundle twice.
	if !m.registrar.IsDeclarationOnly() && !m.localesRegistered {
		if err := collabi18n.Register(); err != nil {
			return err
		}
		m.localesRegistered = true
	}
	if _, err := m.database(); err != nil {
		slog.Warn("conversation routes registered without database", "error", err)
	}
	op := func(id, method, path, summary, tag string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{tag}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.RegisterOwnedTenantMember(m.registrar, api, op("channel.list", http.MethodGet, "/entities/{entity_id}/channels", "List project conversation channels", "entities", 200), authz.EntityPath, authz.ResourceOwnerResolver(m.owner), m.listChannelsHTTP)
	authz.RegisterOwned(m.registrar, api, op("channel.create", http.MethodPost, "/entities/{entity_id}/channels", "Create a conversation channel", "entities", 201), authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "channel", Verb: "post"}, m.createChannelHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, op("channel.messages", http.MethodGet, "/channels/{channel_id}/messages", "Read channel messages and threads", "channels", 200), "channel_id", m.channelOwner, m.listMessagesHTTP)
	authz.RegisterOwned(m.registrar, api, op("channel.post", http.MethodPost, "/channels/{channel_id}/messages", "Post a channel message", "channels", 201), "channel_id", m.channelOwner, authz.Guard{Resource: "channel", Verb: "post"}, m.postMessageHTTP)
	authz.RegisterOwned(m.registrar, api, op("channel.members", http.MethodGet, "/channels/{channel_id}/members", "List visible channel members", "channels", 200), "channel_id", m.channelOwner, authz.Guard{Resource: "channel", Verb: "read"}, m.listMembersHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, op("channel.member_put", http.MethodPut, "/channels/{channel_id}/members/{principal_id}", "Invite a member or change their channel role", "channels", 200), "channel_id", m.channelOwner, m.putMemberHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, op("channel.get", http.MethodGet, "/channels/{channel_id}", "Read authorized channel details", "channels", 200), "channel_id", m.lifecycleChannelOwner, m.channelDetailHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, op("channel.update", http.MethodPatch, "/channels/{channel_id}", "Edit archive or restore a custom channel", "channels", 200), "channel_id", m.lifecycleChannelOwner, m.channelUpdateHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, op("channel.member_delete", http.MethodDelete, "/channels/{channel_id}/members/{principal_id}", "Remove a member or leave a custom channel", "channels", 204), "channel_id", m.lifecycleChannelOwner, m.channelMemberDeleteHTTP)
	authz.RegisterOwned(m.registrar, api, op("channel.read_marker", http.MethodPut, "/channels/{channel_id}/read-marker", "Advance your channel read marker", "channels", 200), "channel_id", m.channelOwner, authz.Guard{Resource: "channel", Verb: "read"}, m.markReadHTTP)
	authz.RegisterOwned(m.registrar, api, op("channel.message_save", http.MethodPut, "/channels/{channel_id}/messages/{message_id}/saved", "Save or unsave a message", "channels", 200), "channel_id", m.channelOwner, authz.Guard{Resource: "channel", Verb: "read"}, m.saveMessageHTTP)
	authz.RegisterOwned(m.registrar, api, op("channel.message_convert", http.MethodPost, "/channels/{channel_id}/messages/{message_id}/work-items", "Convert a message into an authorized work item", "channels", 201), "channel_id", m.channelOwner, authz.Guard{Resource: "channel", Verb: "read"}, m.convertWorkHTTP)
	m.registerConnectorsHTTP(api)
	m.registerMediaHTTP(api)
	m.registerChannelDispatchHTTP(api)
	publishInterruptedError(api)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	if err := authz.RegisterRequestContract(api, "/channels/{channel_id}"); err != nil {
		return err
	}
	if err := authz.RegisterRequestContract(api, "/entities/{entity_id}/channels"); err != nil {
		return err
	}
	if err := authz.RegisterRequestContract(api, "/channels/{channel_id}/messages/{message_id}/work-items"); err != nil {
		return err
	}
	return authz.RegisterRequestContract(api, "/channels/{channel_id}/messages")
}
func conversationHTTPError(ctx context.Context, err error) error {
	var interrupted *Interrupted
	if errors.As(err, &interrupted) {
		return interruptedHTTPError(ctx, interrupted)
	}
	switch {
	case errors.Is(err, ErrSeenSeqRequired):
		return respx.ErrStatus(ctx, 422, 145022, collabi18n.Invalid, nil)
	case errors.Is(err, ErrChannelImmutable):
		return respx.ErrStatus(ctx, 409, 145011, collabi18n.Conflict, nil)
	case errors.Is(err, ErrChannelArchived):
		return respx.ErrStatus(ctx, 409, 145008, collabi18n.Conflict, nil)
	case errors.Is(err, ErrLastOwner):
		return respx.ErrStatus(ctx, 409, 145018, collabi18n.Conflict, nil)
	case errors.Is(err, ErrChannelVersion):
		return respx.ErrStatus(ctx, 409, 145409, collabi18n.Conflict, nil)
	case errors.Is(err, ErrMessageNotEditable):
		return respx.ErrStatus(ctx, 409, 145006, collabi18n.NotEditable, nil)
	case errors.Is(err, ErrMessageVersion):
		return respx.ErrStatus(ctx, 409, 145409, collabi18n.Conflict, nil)
	case errors.Is(err, ErrWorkReadOnly):
		return respx.ErrStatus(ctx, 409, 145409, collabi18n.Conflict, nil)
	case errors.Is(err, ErrForbidden), errors.Is(err, files.ErrPinnedForbidden):
		return respx.ErrStatus(ctx, 403, 145403, collabi18n.Forbidden, nil)
	case errors.Is(err, ErrNotFound), errors.Is(err, files.ErrNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		return respx.ErrStatus(ctx, 404, 145404, collabi18n.NotFound, nil)
	case errors.Is(err, ErrInvalidArgument), errors.Is(err, ErrMemberInactive), errors.Is(err, idempotency.ErrInvalidKey), errors.Is(err, files.ErrPinnedInvalid):
		return respx.ErrStatus(ctx, 422, 145003, collabi18n.Invalid, nil)
	case errors.Is(err, idempotency.ErrKeyReused), errors.Is(err, idempotency.ErrInProgress), errors.Is(err, gorm.ErrDuplicatedKey), errors.Is(err, files.ErrPinnedConflict):
		return respx.ErrStatus(ctx, 409, 145409, collabi18n.Conflict, nil)
	case errors.Is(err, ErrUnavailable), errors.Is(err, files.ErrPinnedUnavailable):
		return respx.ErrStatus(ctx, 503, 145503, collabi18n.Unavailable, nil)
	default:
		return respx.ErrStatus(ctx, 500, 145500, respx.InternalServerError, nil)
	}
}
func (m *Module) listChannelsHTTP(ctx context.Context, in *channelListInput) (*respx.Body[CursorPage[ChannelView]], error) {
	entity, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.database()
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	var page CursorPage[ChannelView]
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		page, e = m.listChannelsStateOn(ctx, tx, authn.TenantIDFromContext(ctx), entity, in.Cursor, in.Limit, in.Archived)
		return e
	})
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	return respx.OK(ctx, page), nil
}

type MessageListOutput struct {
	Body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Meta    struct {
			respx.Meta
			MaxSeq int64               `json:"max_seq"`
			Next   []map[string]string `json:"next"`
		} `json:"meta"`
		Data CursorPage[MessageView] `json:"data"`
	}
}

func (m *Module) listMessagesHTTP(ctx context.Context, in *messageListInput) (*MessageListOutput, error) {
	id, err := guid.RequireID(in.ChannelID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, err := m.projectForChannel(ctx, id)
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	db, err := m.database()
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	var page CursorPage[MessageView]
	var thread *guid.ID
	if in.ThreadID != "" {
		parsed, e := guid.RequireID(in.ThreadID)
		if e != nil {
			return nil, conversationHTTPError(ctx, ErrInvalidArgument)
		}
		thread = &parsed
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		page, e = m.listMessagesOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id, MessageFilter{Cursor: in.Cursor, Limit: in.Limit, AfterSeq: in.AfterSeq, ThreadID: thread, Activity: in.Activity, Search: in.Search})
		return e
	})
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	base := respx.OK(ctx, page).Body
	out := &MessageListOutput{}
	out.Body.Code = base.Code
	out.Body.Message = platformi18n.TContext(ctx, base.Message)
	out.Body.Meta.Meta = base.Meta
	out.Body.Meta.MaxSeq = page.MaxSeq
	out.Body.Meta.Next = []map[string]string{{"operation": "channel.post", "reason": "REPLY_IN_THREAD"}}
	out.Body.Data = page
	return out, nil
}

// conversationCommand uses the existing idempotency store in the same outer
// transaction as business/audit/outbox. Replays recheck membership and IAM.
func conversationCommand[T any](ctx context.Context, m *Module, key, operation, path string, params map[string]string, body any, status int, execute func(*gorm.DB) (T, error), checkReplay func(*gorm.DB, T) error) (*commandOutput[T], error) {
	db, err := m.database()
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	if m.replay == nil {
		return nil, conversationHTTPError(ctx, ErrUnavailable)
	}
	hashBody := body
	if post, ok := body.(PostMessageInput); ok {
		post.SeenSeq = nil
		hashBody = post
	}
	hash, err := idempotency.HashRequest(http.MethodPost, path, params, hashBody)
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	k := idempotency.Key{TenantID: authn.TenantIDFromContext(ctx), SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: key, RequestHash: hash}
	if key != "" {
		if err = k.Validate(); err != nil {
			return nil, conversationHTTPError(ctx, err)
		}
	}
	var value T
	var replayed bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if key == "" {
			var e error
			value, e = execute(tx)
			return e
		}
		result, replayedResult, e := m.replay.Do(ctx, tx, k, func(tx *gorm.DB) (idempotency.Result, error) {
			v, e := execute(tx)
			if e != nil {
				return idempotency.Result{}, e
			}
			encoded, e := json.Marshal(v)
			return idempotency.Result{StatusCode: status, Body: encoded}, e
		})
		if e != nil {
			return e
		}
		replayed = replayedResult
		if e = json.Unmarshal(result.Body, &value); e != nil {
			return e
		}
		if replayed && checkReplay != nil {
			if e := checkReplay(tx, value); e != nil {
				return e
			}
			// A saved command result must never resurrect text withdrawn later.
			if cached, ok := any(value).(MessageView); ok {
				q := query.BindMessages(tx, m.names)
				current, e := q.WithContext(ctx).Where(q.TenantID.Eq(k.TenantID), q.ID.Eq(cached.ID), q.ChannelID.Eq(cached.ChannelID)).First()
				if e != nil {
					return e
				}
				view := messageView(current)
				cached.Body, cached.Retracted, cached.Edited, cached.Version = view.Body, view.Retracted, view.Edited, view.Version
				refreshed, ok := any(cached).(T)
				if !ok {
					return ErrUnavailable
				}
				value = refreshed
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	out := &commandOutput[T]{Body: respx.OK(ctx, value).Body}
	if replayed {
		out.Replayed = "true"
	}
	return out, nil
}
func (m *Module) lifecycleChannelIDs(ctx context.Context, text string) (guid.ID, guid.ID, error) {
	id, err := guid.RequireID(text)
	if err != nil {
		return 0, 0, ErrInvalidArgument
	}
	db, err := m.database()
	if err != nil {
		return 0, 0, err
	}
	q := query.BindChannels(db, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, 0, ErrNotFound
	}
	if err != nil {
		return 0, 0, err
	}
	if row.ProjectID == nil {
		return 0, 0, ErrChannelImmutable
	}
	return id, *row.ProjectID, nil
}

func (m *Module) lifecycleChannelOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, err := m.database()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	q := query.BindChannels(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	if row.ProjectID == nil {
		return authz.ResourceOwner{}, false, nil
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: row.TenantID, EntityID: *row.ProjectID}, true, nil
}

func (m *Module) channelDetailHTTP(ctx context.Context, in *ConversationChannelInput) (*channelDetailOutput, error) {
	id, project, err := m.lifecycleChannelIDs(ctx, in.ChannelID)
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	db, err := m.database()
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	var view ChannelView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, _, e := m.authorizeChannelState(ctx, tx, authn.TenantIDFromContext(ctx), project, id, "channel_read", true, false)
		if e == nil {
			view = channelView(row)
		}
		return e
	})
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	return &channelDetailOutput{ETag: fmt.Sprintf(`"%d"`, view.Version), Body: respx.OK(ctx, view).Body}, nil
}

func (m *Module) channelUpdateHTTP(ctx context.Context, in *channelUpdateHTTPInput) (*commandOutput[ChannelView], error) {
	id, project, err := m.lifecycleChannelIDs(ctx, in.ChannelID)
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	tenant := authn.TenantIDFromContext(ctx)
	return conversationCommand(ctx, m, in.IdempotencyKey, "channel.update", "/channels/{channel_id}", map[string]string{"channel_id": in.ChannelID}, in.Body, 200, func(tx *gorm.DB) (ChannelView, error) {
		return m.updateChannelOn(ctx, tx, tenant, project, id, in.Body)
	}, func(tx *gorm.DB, v ChannelView) error {
		_, member, e := m.authorizeChannelState(ctx, tx, tenant, project, id, "channel_read", true, false)
		if e != nil {
			return e
		}
		return m.channelManagerOn(ctx, tx, tenant, project, member)
	})
}

type channelMemberDeleteInput struct {
	ConversationChannelInput
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) channelMemberDeleteHTTP(ctx context.Context, in *channelMemberDeleteInput) (*struct{}, error) {
	id, project, err := m.lifecycleChannelIDs(ctx, in.ChannelID)
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	target, err := guid.RequireID(in.PrincipalID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.database()
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return m.removeChannelMemberOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id, target)
	})
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	return nil, nil
}

func (m *Module) createChannelHTTP(ctx context.Context, in *channelCreateInput) (*channelCreateOutput, error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	tenant := authn.TenantIDFromContext(ctx)
	created := false
	out, err := conversationCommand(ctx, m, in.IdempotencyKey, "channel.create", "/entities/{entity_id}/channels", map[string]string{"entity_id": in.EntityID}, in.Body, 201, func(tx *gorm.DB) (ChannelView, error) {
		view, err := m.createChannelOn(ctx, tx, tenant, project, in.Body)
		created = view.CreatedNow
		return view, err
	}, func(tx *gorm.DB, v ChannelView) error {
		if in.Body.Kind == "AGENT_DIRECT" {
			return m.checkAgentDirectMembersOn(ctx, tx, tenant, project, v.ID)
		}
		_, _, e := m.authorizeChannel(ctx, tx, tenant, project, v.ID, "channel_post")
		return e
	})
	if err != nil {
		return nil, err
	}
	status := http.StatusCreated
	if in.Body.Kind == "AGENT_DIRECT" && !created {
		status = http.StatusOK
	}
	return &channelCreateOutput{Status: status, Replayed: out.Replayed, Body: out.Body}, nil
}
func (m *Module) postMessageHTTP(ctx context.Context, in *messagePostInput) (*commandOutput[MessageView], error) {
	id, err := guid.RequireID(in.ChannelID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, err := m.projectForChannel(ctx, id)
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	tenant := authn.TenantIDFromContext(ctx)
	return conversationCommand(ctx, m, in.IdempotencyKey, "channel.post", "/channels/{channel_id}/messages", map[string]string{"channel_id": in.ChannelID}, in.Body, 201, func(tx *gorm.DB) (MessageView, error) { return m.postMessageOn(ctx, tx, tenant, project, id, in.Body) }, func(tx *gorm.DB, v MessageView) error {
		q := query.BindChannels(tx, m.names)
		facts, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.ProjectID.Eq(project)).First()
		if e != nil {
			return e
		}
		if facts.Kind == "AGENT_DIRECT" {
			if e = m.checkAgentDirectMembersOn(ctx, tx, tenant, project, id); e != nil {
				return e
			}
		}
		_, member, e := m.authorizeChannel(ctx, tx, tenant, project, id, "channel_post")
		if e == nil && member.External && v.Visibility == "INTERNAL" {
			return ErrNotFound
		}
		return e
	})
}
func (m *Module) putMemberHTTP(ctx context.Context, in *memberPutInput) (*respx.Body[ChannelMemberView], error) {
	id, err := guid.RequireID(in.ChannelID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	principal, err := guid.RequireID(in.PrincipalID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, err := m.projectForChannel(ctx, id)
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	db, err := m.database()
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	var v ChannelMemberView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.putMemberOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id, principal, in.Body)
		return e
	})
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	return respx.OK(ctx, v), nil
}
func (m *Module) markReadHTTP(ctx context.Context, in *readInput) (*respx.Body[int64], error) {
	id, err := guid.RequireID(in.ChannelID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, err := m.projectForChannel(ctx, id)
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	db, err := m.database()
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	var seq int64
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		seq, e = m.markReadOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id, in.Body.Seq)
		return e
	})
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	return respx.OK(ctx, seq), nil
}
func (m *Module) saveMessageHTTP(ctx context.Context, in *saveInput) (*respx.Body[bool], error) {
	id, err := guid.RequireID(in.ChannelID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	message, err := guid.RequireID(in.MessageID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, err := m.projectForChannel(ctx, id)
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	db, err := m.database()
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return m.saveMessageOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id, message, in.Body.Saved)
	})
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	return respx.OK(ctx, in.Body.Saved), nil
}
func (m *Module) listMembersHTTP(ctx context.Context, in *memberListInput) (*respx.Body[CursorPage[ChannelMemberView]], error) {
	id, err := guid.RequireID(in.ChannelID)
	if err != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, err := m.projectForChannel(ctx, id)
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	db, err := m.database()
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	out := CursorPage[ChannelMemberView]{Items: []ChannelMemberView{}}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, _, e := m.authorizeChannelState(ctx, tx, authn.TenantIDFromContext(ctx), project, id, "channel_read", true, false)
		if e != nil {
			return e
		}
		scope := cursorScope("members", id.String(), authn.PrincipalIDFromContext(ctx).String())
		after, e := readCursor(in.Cursor, scope)
		if e != nil {
			return e
		}
		q := query.BindMembers(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ChannelID.Eq(id), q.LeftAt.IsNull(), q.PrincipalID.Gt(guid.ID(after))).Order(q.PrincipalID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		out.HasMore = len(rows) > in.Limit
		if out.HasMore {
			rows = rows[:in.Limit]
		}
		for _, r := range rows {
			out.Items = append(out.Items, ChannelMemberView{PrincipalID: r.PrincipalID, MemberKind: r.MemberKind, Role: r.Role, External: r.External, JoinedAt: r.JoinedAt})
		}
		if out.HasMore {
			out.NextCursor = nextCursor(int64(rows[len(rows)-1].PrincipalID), scope)
		}
		return nil
	})
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

var _ module.HTTPRegistrar = (*Module)(nil)
