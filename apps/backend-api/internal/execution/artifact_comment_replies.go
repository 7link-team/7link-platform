package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const artifactCommentRepliesPath = "/comment-threads/{thread_id}/comments"

var ErrClientThreadReadOnly = errors.New("execution: client thread is read only")
var ErrCommentLimit = errors.New("execution: comment thread limit reached")

type ArtifactCommentReply struct {
	Body    string `json:"body" minLength:"1" maxLength:"8000"`
	SeenSeq *int64 `json:"seen_seq,omitempty" minimum:"0"`
}
type ArtifactCommentMessageView struct {
	EditCount   int        `json:"edit_count"`
	EditedAt    *time.Time `json:"edited_at,omitempty"`
	RetractedAt *time.Time `json:"retracted_at,omitempty"`
	ActorKind   string     `json:"actor_kind"`
	ID          guid.ID    `json:"id"`
	ThreadID    guid.ID    `json:"thread_id"`
	Seq         int64      `json:"seq"`
	Body        *string    `json:"body"`
	CreatedBy   guid.ID    `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
}
type artifactCommentReplyInput struct {
	ThreadID       string `path:"thread_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           ArtifactCommentReply
}
type artifactCommentMessagesInput struct {
	ThreadID string `path:"thread_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	After    int64  `query:"after" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
}
type artifactCommentReplyOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[ArtifactCommentMessageView]
}
type ArtifactCommentMessages struct {
	Items   []ArtifactCommentMessageView `json:"items"`
	LastSeq int64                        `json:"last_seq"`
	HasMore bool                         `json:"has_more"`
}

func commentMessageView(m model.ArtifactCommentMessage) ArtifactCommentMessageView {
	var body *string
	if m.RetractedAt == nil {
		body = &m.Body
	}
	return ArtifactCommentMessageView{EditCount: m.EditCount, EditedAt: m.EditedAt, RetractedAt: m.RetractedAt, ActorKind: m.ActorKind, ID: m.ID, ThreadID: m.ThreadID, Seq: m.Seq, Body: body, CreatedBy: m.CreatedBy, CreatedAt: m.CreatedAt}
}

func (m *Module) commentActorKindOn(ctx context.Context, tx *gorm.DB, tenant, actor guid.ID) (string, error) {
	if m.principals.ActiveHumanByID == nil {
		return "", ErrCapabilityUnavailable
	}
	_, human, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return "", err
	}
	if human {
		return "HUMAN", nil
	}
	if m.agents.ByPrincipalOn != nil {
		a, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, actor)
		if err != nil {
			return "", err
		}
		if found && a.TenantID == tenant && a.PrincipalID == actor {
			return "AGENT", nil
		}
	}
	return "PRINCIPAL", nil
}
func (m *Module) commentThreadOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, locked bool) (model.ArtifactCommentThread, error) {
	q := query.BindArtifactCommentThreads(tx, m.names)
	source := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id))
	if locked {
		source = source.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	row, err := source.First()
	if err != nil {
		return model.ArtifactCommentThread{}, err
	}
	return *row, nil
}
func (m *Module) checkCommentThreadSeenOn(ctx context.Context, tx *gorm.DB, thread model.ArtifactCommentThread, seen *int64, required bool) error {
	if seen == nil {
		if required {
			return delivery.ErrWorkSeenSeqRequired
		}
		return nil
	}
	if *seen < 0 || *seen > int64(thread.Version) {
		return ErrInvalidArgument
	}
	if *seen == int64(thread.Version) {
		return nil
	}
	actor := authn.PrincipalIDFromContext(ctx)
	mq := query.BindArtifactCommentMessages(tx, m.names)
	messages, err := mq.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(mq.TenantID.Eq(thread.TenantID), mq.ThreadID.Eq(thread.ID), mq.Seq.Gt(*seen), mq.CreatedBy.Neq(actor)).Order(mq.Seq).Limit(51).Find()
	if err != nil {
		return err
	}
	cq := query.BindArtifactCommentConversions(tx, m.names)
	conversions, err := cq.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(cq.TenantID.Eq(thread.TenantID), cq.ThreadID.Eq(thread.ID), cq.Seq.Gt(*seen), cq.ActorID.Neq(actor)).Order(cq.Seq).Limit(51).Find()
	if err != nil {
		return err
	}
	events := []delivery.WorkActivityView{}
	var changes []model.ArtifactCommentEvent
	if err = tx.WithContext(ctx).Table(m.names.CommentEvents.String()).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND thread_id = ? AND seq > ? AND actor_id <> ?", thread.TenantID, thread.ID, *seen, actor).Order("seq ASC").Limit(51).Find(&changes).Error; err != nil {
		return err
	}
	for _, v := range changes {
		events = append(events, delivery.WorkActivityView{Seq: v.Seq, Kind: "STATE_CHANGED", Actor: delivery.WorkActivityActor{Kind: v.ActorKind, ID: v.ActorID}, Ref: delivery.WorkActivityRef{Kind: "artifact_comment", ID: thread.ID}, OccurredAt: v.CreatedAt})
	}
	for _, v := range messages {
		events = append(events, delivery.WorkActivityView{Seq: v.Seq, Kind: "COMMENT_ADDED", Actor: delivery.WorkActivityActor{Kind: v.ActorKind, ID: v.CreatedBy}, Ref: delivery.WorkActivityRef{Kind: "artifact_comment_message", ID: v.ID}, OccurredAt: v.CreatedAt})
	}
	for _, v := range conversions {
		events = append(events, delivery.WorkActivityView{Seq: v.Seq, Kind: "STATE_CHANGED", Actor: delivery.WorkActivityActor{Kind: v.ActorKind, ID: v.ActorID}, Ref: delivery.WorkActivityRef{Kind: "artifact_comment", ID: thread.ID}, OccurredAt: v.CreatedAt})
	}
	if len(events) == 0 {
		return nil
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	truncated := len(events) > 50
	if truncated {
		events = events[:50]
	}
	return &delivery.WorkWriteInterrupted{State: "interrupted", Stream: "comment_thread:" + thread.ID.String(), SeenSeq: *seen, CurrentSeq: int64(thread.Version), NewerEvents: events, Truncated: truncated}
}
func (m *Module) ReplyArtifactCommentOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in ArtifactCommentReply) (ArtifactCommentMessageView, error) {
	var empty ArtifactCommentMessageView
	if err := transaction(tx, tenant, id); err != nil {
		return empty, err
	}
	thread, err := m.commentThreadOn(ctx, tx, tenant, id, false)
	if err != nil {
		return empty, err
	}
	kind, err := m.commentAuthority(ctx, tx, tenant, thread.ProjectID, "artifact_comment_write")
	if err != nil {
		return empty, err
	}
	row, err := m.rowOn(ctx, tx, tenant, thread.ExecutionID, false)
	if err != nil {
		return empty, err
	}
	// Work before thread matches canonical conversion and work activity locking.
	if err = m.checkExecutionContextOn(ctx, tx, row, nil, false); err != nil {
		return empty, err
	}
	thread, err = m.commentThreadOn(ctx, tx, tenant, id, true)
	if err != nil {
		return empty, err
	}
	if err = m.checkCommentThreadSeenOn(ctx, tx, thread, in.SeenSeq, kind == "AGENT"); err != nil {
		return empty, err
	}
	if thread.Visibility != "INTERNAL" {
		return empty, ErrClientThreadReadOnly
	}
	if !utf8.ValidString(in.Body) || strings.TrimSpace(in.Body) == "" || utf8.RuneCountInString(in.Body) > 8000 {
		return empty, ErrInvalidArgument
	}
	if thread.Version == math.MaxInt32 {
		return empty, ErrConflict
	}
	mq := query.BindArtifactCommentMessages(tx, m.names)
	existing, err := mq.WithContext(ctx).Select(mq.ID).Clauses(clause.Locking{Strength: "UPDATE"}).Where(mq.TenantID.Eq(tenant), mq.ThreadID.Eq(id)).Limit(201).Find()
	if err != nil {
		return empty, err
	}
	if len(existing) >= 200 {
		return empty, ErrCommentLimit
	}
	mid, err := m.nextID()
	if err != nil {
		return empty, err
	}
	message := model.ArtifactCommentMessage{ActorKind: kind, ID: mid, TenantID: tenant, ThreadID: id, Seq: int64(thread.Version) + 1, Body: in.Body, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if err = mq.WithContext(ctx).Create(&message); err != nil {
		return empty, err
	}
	tq := query.BindArtifactCommentThreads(tx, m.names)
	if _, err = tq.WithContext(ctx).Where(tq.TenantID.Eq(tenant), tq.ID.Eq(id)).UpdateSimple(tq.Version.Value(thread.Version+1), tq.State.Value("OPEN")); err != nil {
		return empty, err
	}
	if err = m.recordExecutionActivityOn(ctx, tx, row, "COMMENT_ADDED", "artifact_comment", id, kind); err != nil {
		return empty, err
	}
	if err = m.eventOn(ctx, tx, row, "execution.artifact_comment_added", kind, "permission:artifact_comment_write", map[string]any{"thread_id": id.String(), "message_id": mid.String(), "thread_seq": message.Seq, "kind": "COMMENT_ADDED"}); err != nil {
		return empty, err
	}
	return commentMessageView(message), nil
}
func (m *Module) artifactCommentThreadOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, err := m.writer()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	tq := query.BindArtifactCommentThreads(db, m.names)
	thread, err := tq.WithContext(ctx).Where(tq.ID.Eq(id)).First()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: thread.TenantID, EntityID: thread.ProjectID}, true, nil
}
func (m *Module) registerArtifactCommentRepliesHTTP(api huma.API) error {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "artifact_comment.reply", Method: http.MethodPost, Path: artifactCommentRepliesPath, Summary: "Reply to an internal artifact comment thread", Tags: []string{"executions"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 503}}, "thread_id", m.artifactCommentThreadOwner, authz.Guard{Resource: "artifact_comment", Verb: "write"}, m.replyArtifactCommentHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "artifact_comment.messages", Method: http.MethodGet, Path: artifactCommentRepliesPath, Summary: "Read comment messages and the current thread cursor", Tags: []string{"executions"}, Errors: []int{401, 403, 404, 409, 422, 503}}, "thread_id", m.artifactCommentThreadOwner, authz.Guard{Resource: "execution", Verb: "read"}, m.artifactCommentMessagesHTTP)
	return authz.RegisterRequestContract(api, artifactCommentRepliesPath)
}
func (m *Module) replyArtifactCommentHTTP(ctx context.Context, in *artifactCommentReplyInput) (*artifactCommentReplyOutput, error) {
	id, err := guid.RequireID(in.ThreadID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.commentIdempotency == nil {
		return nil, httpError(ctx, ErrCapabilityUnavailable)
	}
	digest, err := idempotency.HashRequest(http.MethodPost, artifactCommentRepliesPath, map[string]string{"thread_id": id.String()}, in.Body)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	tenant := authn.TenantIDFromContext(ctx)
	key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "artifact_comment.reply", Value: in.IdempotencyKey, RequestHash: digest}
	if err = key.Validate(); err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	var result idempotency.Result
	var replay bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		thread, e := m.commentThreadOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		if _, e = m.commentAuthority(ctx, tx, tenant, thread.ProjectID, "artifact_comment_write"); e != nil {
			return e
		}
		result, replay, e = m.commentIdempotency.Do(ctx, tx, key, func(tx *gorm.DB) (idempotency.Result, error) {
			message, e := m.ReplyArtifactCommentOn(ctx, tx, tenant, id, in.Body)
			if e != nil {
				return idempotency.Result{}, e
			}
			body, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, message).Body)
			return idempotency.Result{StatusCode: 201, Body: body, ResourceKind: "artifact_comment_message", ResourceID: message.ID}, e
		})
		return e
	})
	if err != nil {
		return nil, writeContextHTTPError(ctx, err, "artifact_comment.reply")
	}
	out := &artifactCommentReplyOutput{Status: 201}
	if replay {
		out.Replayed = "true"
	}
	if err = json.Unmarshal(result.Body, &out.Body); err != nil {
		return nil, httpError(ctx, err)
	}
	return out, nil
}
func (m *Module) artifactCommentMessagesHTTP(ctx context.Context, in *artifactCommentMessagesInput) (*respx.Body[ArtifactCommentMessages], error) {
	id, err := guid.RequireID(in.ThreadID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	out := ArtifactCommentMessages{Items: []ArtifactCommentMessageView{}}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		thread, e := m.commentThreadOn(ctx, tx, authn.TenantIDFromContext(ctx), id, false)
		if e != nil {
			return e
		}
		if _, e = m.commentAuthority(ctx, tx, thread.TenantID, thread.ProjectID, "execution_read"); e != nil {
			return e
		}
		thread, e = m.commentThreadOn(ctx, tx, thread.TenantID, id, true)
		if e != nil {
			return e
		}
		if in.After < 0 || in.After > int64(thread.Version) || in.Limit < 1 || in.Limit > 100 {
			return ErrInvalidArgument
		}
		mq := query.BindArtifactCommentMessages(tx, m.names)
		messages, e := mq.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(mq.TenantID.Eq(thread.TenantID), mq.ThreadID.Eq(id), mq.Seq.Gt(in.After)).Order(mq.Seq).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		out.LastSeq = int64(thread.Version)
		out.HasMore = len(messages) > in.Limit
		if out.HasMore {
			messages = messages[:in.Limit]
		}
		for _, message := range messages {
			out.Items = append(out.Items, commentMessageView(*message))
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
