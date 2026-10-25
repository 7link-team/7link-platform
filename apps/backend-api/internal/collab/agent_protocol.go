package collab

import (
	"7link.com/platform/internal/collab/i18n"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gen"
	"gorm.io/gorm"
	"reflect"
)

// Interrupted carries only currently visible messages from the target stream.
type Interrupted struct {
	State       string        `json:"state"`
	Stream      string        `json:"stream"`
	SeenSeq     int64         `json:"seen_seq"`
	CurrentSeq  int64         `json:"current_seq"`
	NewerEvents []MessageView `json:"newer_events"`
	Truncated   bool          `json:"truncated"`
	Since       int64         `json:"since"`
	Head        int64         `json:"head"`
	Next        []string      `json:"next"`
}

// The channel lock is already held. Only other authors' visible updates in
// the actual target stream interrupt a write; unrelated replies do not.
var ErrSeenSeqRequired = errors.New("collab: seen_seq required")

func (m *Module) checkSeenOn(ctx context.Context, tx *gorm.DB, tenant, channel, actor guid.ID, external bool, head int64, in PostMessageInput) error {
	if in.SeenSeq == nil {
		return ErrSeenSeqRequired
	}
	if *in.SeenSeq < 0 || *in.SeenSeq > head {
		return ErrInvalidArgument
	}
	q := query.BindMessages(tx, m.names)
	request := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel), q.AuthorID.Neq(actor))
	stream := "channel:" + channel.String()
	if in.ReplyToMessageID == nil {
		// A withdrawn top-level message is no longer context to act on. Thread
		// roots retain their separate withdrawal semantics.
		request = request.Where(q.Seq.Gt(*in.SeenSeq), q.ReplyToMessageID.IsNull(), q.WithdrawnAt.IsNull())
		cards := query.BindSystemCardRecords(tx, m.names)
		request = request.Not(gen.Exists(cards.WithContext(ctx).Where(cards.TenantID.Eq(tenant), cards.MessageID.EqCol(q.ID), cards.Kind.In("EVENT", "SEARCH_CARD"))))
	} else {
		stream = "thread:" + in.ReplyToMessageID.String()
		request = request.Where(q.WithContext(ctx).Where(q.ID.Eq(*in.ReplyToMessageID)).Or(q.ReplyToMessageID.Eq(*in.ReplyToMessageID)))
		request = request.Where(q.WithContext(ctx).Where(q.Seq.Gt(*in.SeenSeq)).Or(q.WithContext(ctx).Where(q.ID.Eq(*in.ReplyToMessageID), q.ContextSeq.Gt(*in.SeenSeq))))
	}
	if external {
		request = request.Where(q.Visibility.Eq("CHANNEL"))
	}
	rows, err := request.Order(q.Seq).Limit(51).Find()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	if external || in.ReplyToMessageID != nil {
		latestRequest := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel))
		if external {
			latestRequest = latestRequest.Where(q.Visibility.Eq("CHANNEL"))
		}
		if in.ReplyToMessageID != nil {
			latestRequest = latestRequest.Where(q.WithContext(ctx).Where(q.ID.Eq(*in.ReplyToMessageID)).Or(q.ReplyToMessageID.Eq(*in.ReplyToMessageID)))
		} else {
			latestRequest = latestRequest.Where(q.ReplyToMessageID.IsNull())
		}
		latest, e := latestRequest.Order(q.ContextSeq.Desc(), q.Seq.Desc()).Limit(1).Find()
		if e != nil {
			return e
		}
		head = max(latest[0].Seq, latest[0].ContextSeq)
	}
	truncated := len(rows) > 50
	if truncated {
		rows = rows[:50]
	}
	newer := make([]MessageView, 0, len(rows))
	for _, row := range rows {
		newer = append(newer, messageView(row))
	}
	if err = m.hydrateSystemCardsOn(tx, tenant, newer); err != nil {
		return err
	}
	return &Interrupted{State: "interrupted", Stream: stream, SeenSeq: *in.SeenSeq, CurrentSeq: head, Since: *in.SeenSeq, Head: head, NewerEvents: newer, Truncated: truncated, Next: []string{"REREAD_CONTEXT", "RETRY_WITH_SAME_IDEMPOTENCY_KEY"}}
}

func (*Interrupted) Error() string { return "collab: interrupted" }

type interruptedResponse struct {
	huma.StatusError
	body json.RawMessage
}

func (e *interruptedResponse) MarshalJSON() ([]byte, error) { return e.body, nil }
func interruptedHTTPError(ctx context.Context, interrupted *Interrupted) error {
	base := respx.ErrStatus(ctx, 409, 145021, collabi18n.Conflict, interrupted)
	raw, err := respx.MarshalEnvelope(ctx, base)
	if err != nil {
		return base
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return base
	}
	meta, ok := body["meta"].(map[string]any)
	if !ok {
		return base
	}
	meta["next"] = []map[string]string{{"operation": "inbox_events.list", "reason": "READ_NEWER_EVENTS"}, {"operation": "channel.post", "reason": "RETRY_WITH_SAME_IDEMPOTENCY_KEY"}}
	raw, err = json.Marshal(body)
	if err != nil {
		return base
	}
	var status huma.StatusError
	if !errors.As(base, &status) {
		return base
	}
	return &interruptedResponse{StatusError: status, body: raw}
}

// MessageObserverConnector lets a downstream inbox module project authorized
// recipients in the message transaction, without reversing dependencies.
type MessageReference struct {
	TenantID, AgentPrincipalID, MessageID, ChannelID guid.ID
	ThreadID                                         *guid.ID
	Seq                                              int64
	Kind                                             string
}
type MessageObserver func(context.Context, *gorm.DB, MessageReference) error
type MessageObserverConnector struct{ Bind func(MessageObserver) error }
type ThreadAccess struct {
	CheckOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (int64, error)
}

func (m *Module) bindMessageObserver(fn MessageObserver) error {
	if fn == nil || m.messageObserver != nil {
		return ErrInvalidArgument
	}
	m.messageObserver = fn
	return nil
}
func (m *Module) threadAccessOn(ctx context.Context, tx *gorm.DB, tenant, project, thread, target guid.ID) (int64, error) {
	if project == 0 {
		return m.teamThreadAccessOn(ctx, tx, tenant, thread, target)
	}
	if tx == nil || tenant <= 0 || project < 0 || thread <= 0 || target <= 0 {
		return 0, ErrInvalidArgument
	}
	q := query.BindMessages(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(thread), q.ReplyToMessageID.IsNull(), q.WithdrawnAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	ch, member, err := m.authorizeChannel(ctx, tx, tenant, project, r.ChannelID, "channel_read")
	if err != nil {
		return 0, err
	}
	if member.External && r.Visibility == "INTERNAL" {
		return 0, ErrNotFound
	}
	cm := query.BindMembers(tx, m.names)
	recipient, err := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(r.ChannelID), cm.PrincipalID.Eq(target), cm.LeftAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if recipient.External && (r.Visibility == "INTERNAL" || ch.Visibility != "CLIENT_SHARED") {
		return 0, ErrNotFound
	}
	stream := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(r.ChannelID), q.WithContext(ctx).Where(q.ID.Eq(thread)).Or(q.ReplyToMessageID.Eq(thread)))
	if member.External || recipient.External {
		stream = stream.Where(q.Visibility.Eq("CHANNEL"))
	}
	latest, err := stream.Order(q.ContextSeq.Desc(), q.Seq.Desc()).Limit(1).Find()
	if err != nil {
		return 0, err
	}
	if len(latest) == 0 {
		return 0, ErrNotFound
	}
	return max(latest[0].Seq, latest[0].ContextSeq), nil
}

type InterruptedErrorEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Meta    struct {
		respx.Meta
		Next []map[string]string `json:"next"`
	} `json:"meta"`
	Data *Interrupted `json:"data"`
}

func publishInterruptedError(api huma.API) {
	item := api.OpenAPI().Paths["/channels/{channel_id}/messages"]
	if item == nil || item.Post == nil {
		return
	}
	op := item.Post
	response := op.Responses["409"]
	if response == nil {
		return
	}
	media := response.Content["application/json"]
	if media == nil {
		return
	}
	schema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[InterruptedErrorEnvelope](), true, "ConversationInterruptedErrorEnvelope")
	copied := *response
	content := *media
	content.Schema = &huma.Schema{AnyOf: []*huma.Schema{media.Schema, schema}}
	copied.Content = map[string]*huma.MediaType{"application/json": &content}
	op.Responses["409"] = &copied
}
