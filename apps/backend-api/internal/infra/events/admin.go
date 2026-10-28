package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/infra/audit"
	eventsi18n "7link.com/platform/internal/infra/events/i18n"
	"7link.com/platform/internal/infra/events/model"
	"7link.com/platform/internal/infra/events/query"
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

const replayProofPurpose = "system_event_replay"

func adminHTTPError(ctx context.Context, err error) error {
	status, code, key := 503, 171503, respx.ServiceUnavailable
	switch {
	case errors.Is(err, errAdminForbidden), errors.Is(err, authn.ErrInvalidCredentials):
		status, code, key = 403, 171403, respx.Forbidden
	case errors.Is(err, ErrSubscriberNotFound), errors.Is(err, ErrParkedNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		status, code, key = 404, 171404, respx.NotFound
	case errors.Is(err, ErrVersionConflict), errors.Is(err, idempotency.ErrKeyReused):
		status, code, key = 409, 171409, eventsi18n.VersionConflict
	case errors.Is(err, ErrParkedConflict):
		status, code, key = 409, 171420, eventsi18n.InvalidTransition
	case errors.Is(err, ErrReplayActive), errors.Is(err, idempotency.ErrInProgress):
		status, code, key = 409, 171422, eventsi18n.ReplayActive
	case errors.Is(err, ErrReplayRange):
		status, code, key = 400, 171401, eventsi18n.ReplayRange
	case errors.Is(err, errAdminInvalid), errors.Is(err, ErrInvalidEvent), errors.Is(err, idempotency.ErrInvalidKey):
		status, code, key = 400, 171400, respx.InvalidArgument
	}
	return respx.ErrStatus(ctx, status, code, key, nil)
}
func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil {
		return ErrUnavailable
	}
	if m.admin.registrar == nil {
		slog.Warn("event administration HTTP is disabled; enable IAM to bind platform authorization")
		return nil
	}
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"domain-events"}, Errors: []int{400, 401, 403, 404, 409, 422, 428, 503}}
	}
	view, manage := authz.Guard{Resource: "system_event", Verb: "view"}, authz.Guard{Resource: "system_event", Verb: "manage"}
	authz.RegisterPlatform(m.admin.registrar, api, op("events.subscribers.list", http.MethodGet, "/platform/domain-event-subscribers", "List event subscriber cursors and backlog"), view, m.listAdminSubscribers)
	authz.RegisterPlatform(m.admin.registrar, api, op("events.list", http.MethodGet, "/platform/domain-events", "List domain event envelopes without data"), view, m.listAdminEvents)
	authz.RegisterPlatform(m.admin.registrar, api, op("events.get", http.MethodGet, "/platform/domain-events/{event_id}", "Read a domain event and record the sensitive read"), view, m.getAdminEvent)
	authz.RegisterPlatform(m.admin.registrar, api, op("events.parked.list", http.MethodGet, "/platform/domain-event-subscribers/{subscriber}/parked-events", "List a subscriber's parked events"), view, m.listAdminParked)
	authz.RegisterPlatform(m.admin.registrar, api, op("events.parked.retry", http.MethodPost, "/platform/parked-events/{parked_id}/attempts", "Retry a parked event"), manage, func(ctx context.Context, in *adminParkedMutationInput) (*adminParkedOutput, error) {
		return m.mutateAdminParked(ctx, in, true)
	})
	authz.RegisterPlatform(m.admin.registrar, api, op("events.parked.discard", http.MethodPatch, "/platform/parked-events/{parked_id}", "Discard a parked event with a reason"), manage, func(ctx context.Context, in *adminParkedMutationInput) (*adminParkedOutput, error) {
		return m.mutateAdminParked(ctx, in, false)
	})
	replay := op("events.replay.create", http.MethodPost, "/platform/domain-event-subscribers/{subscriber}/replays", "Replay retained events using fresh browser reauthentication")
	replay.DefaultStatus = http.StatusCreated
	authz.RegisterPlatform(m.admin.registrar, api, replay, manage, m.replayAdminEvents)
	authz.RegisterPlatform(m.admin.registrar, api, op("events.subscribers.update", http.MethodPatch, "/platform/domain-event-subscribers/{subscriber}", "Pause or resume an event subscriber"), manage, m.patchAdminSubscriber)
	return nil
}

var _ module.HTTPRegistrar = (*Module)(nil)

type adminEventActor struct {
	Kind            string   `json:"kind"`
	ID              *guid.ID `json:"id,omitempty"`
	AuthoritySource string   `json:"authority_source"`
}
type adminEventSubject struct {
	Kind string  `json:"kind"`
	ID   guid.ID `json:"id"`
}
type adminEventView struct {
	Module         string            `json:"module,omitempty"`
	DisplayName    string            `json:"display_name,omitempty"`
	Description    string            `json:"description,omitempty"`
	EventID        guid.ID           `json:"event_id"`
	EventType      string            `json:"event_type"`
	SchemaVersion  int               `json:"schema_version"`
	StreamSeq      *int64            `json:"stream_seq"`
	TenantID       guid.ID           `json:"tenant_id"`
	ProjectID      *guid.ID          `json:"project_id,omitempty"`
	OccurredAt     time.Time         `json:"occurred_at"`
	Actor          adminEventActor   `json:"actor"`
	Subject        adminEventSubject `json:"subject"`
	SubjectVersion int64             `json:"subject_version"`
	Topics         []string          `json:"topics"`
	Data           json.RawMessage   `json:"data,omitempty"`
	CorrelationID  string            `json:"correlation_id"`
	CausationID    *guid.ID          `json:"causation_id,omitempty"`
}

func (m *Module) adminEvent(ctx context.Context, row *model.EventRow, detail bool) (adminEventView, error) {
	v := adminEventView{EventID: row.ID, EventType: row.EventType, SchemaVersion: row.SchemaVersion, StreamSeq: row.StreamSeq, TenantID: row.TenantID, ProjectID: row.ProjectID, OccurredAt: row.OccurredAt, Actor: adminEventActor{row.ActorKind, row.ActorID, row.AuthoritySource}, Subject: adminEventSubject{row.SubjectKind, row.SubjectID}, SubjectVersion: row.SubjectVersion, Topics: []string{}, CorrelationID: row.CorrelationID, CausationID: row.CausationID}
	m.mu.RLock()
	spec := m.types[row.EventType]
	m.mu.RUnlock()
	v.Module = spec.Module
	if spec.DisplayNameKey != "" {
		v.DisplayName = platformi18n.TContext(ctx, spec.DisplayNameKey)
	}
	if spec.DescriptionKey != "" {
		v.Description = platformi18n.TContext(ctx, spec.DescriptionKey)
	}
	if err := json.Unmarshal([]byte(row.Topics), &v.Topics); err != nil {
		return v, err
	}
	if detail {
		raw, err := audit.RedactAdministrativeJSON([]byte(row.Data))
		if err != nil {
			return v, err
		}
		v.Data = json.RawMessage(raw)
	}
	return v, nil
}

type adminEventListInput struct {
	Offset      int    `query:"offset" default:"0" minimum:"0"`
	Limit       int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	EventType   string `query:"event_type" maxLength:"99"`
	Tenant      string `query:"tenant_id" maxLength:"24"`
	SubjectKind string `query:"subject_kind" maxLength:"67"`
	SubjectID   string `query:"subject_id" maxLength:"24"`
	StreamSeq   string `query:"stream_seq" maxLength:"24"`
	OccurredAt  string `query:"occurred_at" maxLength:"64"`
}

func adminLimit(offset, limit int) (int, error) {
	if limit == 0 {
		limit = 50
	}
	if offset < 0 || limit < 1 || limit > 200 {
		return 0, errAdminInvalid
	}
	return limit, nil
}
func adminEqual(raw string) string { return strings.TrimPrefix(raw, "eq:") }
func adminNumber(raw string, zero bool) (int64, error) {
	v, e := strconv.ParseInt(adminEqual(raw), 10, 64)
	if e != nil || v < 0 || (!zero && v == 0) {
		return 0, errAdminInvalid
	}
	return v, nil
}
func (m *Module) listAdminEvents(ctx context.Context, in *adminEventListInput) (*respx.Body[[]adminEventView], error) {
	limit, err := adminLimit(in.Offset, in.Limit)
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	out := []adminEventView{}
	var total int64
	err = m.adminTransaction(ctx, false, func(tx *gorm.DB) error {
		q := query.BindWithNames(tx, m.names)
		scope := q.WithContext(ctx)
		if in.EventType != "" {
			scope = scope.Where(q.EventType.Eq(adminEqual(in.EventType)))
		}
		if in.SubjectKind != "" {
			scope = scope.Where(q.SubjectKind.Eq(adminEqual(in.SubjectKind)))
		}
		if in.Tenant != "" {
			v, e := adminNumber(in.Tenant, true)
			if e != nil {
				return e
			}
			scope = scope.Where(q.TenantID.Eq(guid.ID(v)))
		}
		if in.SubjectID != "" {
			v, e := adminNumber(in.SubjectID, false)
			if e != nil {
				return e
			}
			scope = scope.Where(q.SubjectID.Eq(guid.ID(v)))
		}
		if in.StreamSeq != "" {
			op, raw, ok := strings.Cut(in.StreamSeq, ":")
			if !ok {
				return errAdminInvalid
			}
			v, e := adminNumber(raw, true)
			if e != nil {
				return e
			}
			switch op {
			case "gt":
				scope = scope.Where(q.StreamSeq.Gt(v))
			case "lt":
				scope = scope.Where(q.StreamSeq.Lt(v))
			default:
				return errAdminInvalid
			}
		}
		if in.OccurredAt != "" {
			op, raw, ok := strings.Cut(in.OccurredAt, ":")
			if !ok {
				return errAdminInvalid
			}
			at, e := time.Parse(time.RFC3339Nano, raw)
			if e != nil {
				return errAdminInvalid
			}
			switch op {
			case "gt":
				scope = scope.Where(q.OccurredAt.Gt(at.UTC()))
			case "gte":
				scope = scope.Where(q.OccurredAt.Gte(at.UTC()))
			case "lt":
				scope = scope.Where(q.OccurredAt.Lt(at.UTC()))
			case "lte":
				scope = scope.Where(q.OccurredAt.Lte(at.UTC()))
			default:
				return errAdminInvalid
			}
		}
		var e error
		total, e = scope.Session(&gorm.Session{}).Count()
		if e != nil {
			return e
		}
		rows, e := scope.Session(&gorm.Session{}).Order(q.StreamSeq.Desc(), q.ID.Desc()).Offset(in.Offset).Limit(limit).Find()
		if e != nil {
			return e
		}
		for _, row := range rows {
			v, e := m.adminEvent(ctx, row, false)
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return respx.List(ctx, out, respx.Page{Offset: in.Offset, Limit: limit, Count: len(out), Total: total}), nil
}

type adminEventInput struct {
	EventID string `path:"event_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) getAdminEvent(ctx context.Context, in *adminEventInput) (*respx.Body[adminEventView], error) {
	id, err := guid.RequireID(in.EventID)
	if err != nil {
		return nil, adminHTTPError(ctx, errAdminInvalid)
	}
	audit.Annotate(ctx, audit.Action("events.detail_read"), audit.Resource("domain_event", id.String()), audit.SetAccess(audit.AccessWrite))
	var out adminEventView
	err = m.adminTransaction(ctx, false, func(tx *gorm.DB) error {
		q := query.BindWithNames(tx, m.names)
		r, e := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
		if e != nil {
			return e
		}
		out, e = m.adminEvent(ctx, r, true)
		if e != nil {
			return e
		}
		return m.adminAuditOn(ctx, tx, "events.detail_read", r.TenantID, "domain_event", id, map[string]any{"read": true})
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

type AdminSubscriberInput struct {
	Subscriber string `path:"subscriber" minLength:"1" maxLength:"96"`
}
type adminCursorView struct {
	Partition int   `json:"p"`
	LastSeq   int64 `json:"last_seq"`
	Lag       int64 `json:"lag"`
}
type adminSubscriberView struct {
	Module      string            `json:"module,omitempty"`
	DisplayName string            `json:"display_name,omitempty"`
	Description string            `json:"description,omitempty"`
	EventTypes  []string          `json:"event_types"`
	Name        string            `json:"name"`
	Mode        DeliveryMode      `json:"mode"`
	Partitions  int               `json:"partitions"`
	Cursors     []adminCursorView `json:"cursors"`
	Parked      int64             `json:"parked"`
	Blocked     int64             `json:"blocked"`
	Paused      bool              `json:"paused"`
}

func (m *Module) adminSubscriber(ctx context.Context, s SubscriberStatus) adminSubscriberView {
	v := adminSubscriberView{Name: s.Name, Mode: s.Mode, Partitions: s.Partitions, Cursors: []adminCursorView{}, Parked: s.Parked, Blocked: s.Blocked, Paused: s.Paused}
	m.mu.RLock()
	declaration := m.subscribers[s.Name]
	m.mu.RUnlock()
	v.Module = declaration.Module
	v.EventTypes = append([]string{}, declaration.EventTypes...)
	if declaration.DisplayNameKey != "" {
		v.DisplayName = platformi18n.TContext(ctx, declaration.DisplayNameKey)
	}
	if declaration.DescriptionKey != "" {
		v.Description = platformi18n.TContext(ctx, declaration.DescriptionKey)
	}
	for _, c := range s.Cursors {
		v.Cursors = append(v.Cursors, adminCursorView(c))
	}
	return v
}
func (m *Module) listAdminSubscribers(ctx context.Context, _ *struct{}) (*respx.Body[[]adminSubscriberView], error) {
	out := []adminSubscriberView{}
	err := m.adminTransaction(ctx, false, func(tx *gorm.DB) error {
		rows, e := m.SubscribersOn(ctx, tx)
		if e != nil {
			return e
		}
		for _, r := range rows {
			out = append(out, m.adminSubscriber(ctx, r))
		}
		return nil
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

type adminSubscriberPatchInput struct {
	AdminSubscriberInput
	Body struct {
		Paused bool `json:"paused"`
	}
}

func (m *Module) patchAdminSubscriber(ctx context.Context, in *adminSubscriberPatchInput) (*respx.Body[adminSubscriberView], error) {
	var out adminSubscriberView
	err := m.adminTransaction(ctx, true, func(tx *gorm.DB) error {
		if e := m.SetPausedOn(ctx, tx, in.Subscriber, in.Body.Paused); e != nil {
			return e
		}
		rows, e := m.SubscribersOn(ctx, tx)
		if e != nil {
			return e
		}
		for _, r := range rows {
			if r.Name == in.Subscriber {
				out = m.adminSubscriber(ctx, r)
				return m.adminAuditOn(ctx, tx, "events.subscriber_paused", 0, "domain_event_subscriber", 0, map[string]any{"subscriber": in.Subscriber, "paused": in.Body.Paused})
			}
		}
		return ErrSubscriberNotFound
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

type adminParkedView struct {
	ID              guid.ID    `json:"id"`
	Consumer        string     `json:"consumer"`
	PartitionNo     int        `json:"partition_no"`
	EventID         guid.ID    `json:"event_id"`
	StreamSeq       int64      `json:"stream_seq"`
	TenantID        guid.ID    `json:"tenant_id"`
	SubjectKind     string     `json:"subject_kind"`
	SubjectID       guid.ID    `json:"subject_id"`
	Reason          string     `json:"reason"`
	State           string     `json:"state"`
	Attempts        int        `json:"attempts"`
	NextAttemptAtMS *int64     `json:"next_attempt_at_ms,omitempty"`
	LastErrorCode   string     `json:"last_error_code"`
	LastError       string     `json:"last_error"`
	ResolvedBy      *guid.ID   `json:"resolved_by,omitempty"`
	ResolutionNote  string     `json:"resolution_note"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	Version         int        `json:"version"`
}

func adminParked(r *model.Parked) adminParkedView {
	return adminParkedView{r.ID, r.Consumer, r.PartitionNo, r.EventID, r.StreamSeq, r.TenantID, r.SubjectKind, r.SubjectID, r.Reason, r.State, r.Attempts, r.NextAttemptAtMS, r.LastErrorCode, r.LastError, r.ResolvedBy, r.ResolutionNote, r.CreatedAt, r.UpdatedAt, r.ResolvedAt, r.Version}
}

type adminParkedListInput struct {
	AdminSubscriberInput
	Offset      int    `query:"offset" default:"0" minimum:"0"`
	Limit       int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	State       string `query:"state" enum:",PARKED,REDELIVERING,DELIVERED,DISCARDED"`
	Reason      string `query:"reason" enum:",FAILED,BLOCKED"`
	SubjectKind string `query:"subject_kind" maxLength:"67"`
}

func (m *Module) listAdminParked(ctx context.Context, in *adminParkedListInput) (*respx.Body[[]adminParkedView], error) {
	limit, err := adminLimit(in.Offset, in.Limit)
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	out := []adminParkedView{}
	var total int64
	err = m.adminTransaction(ctx, false, func(tx *gorm.DB) error {
		m.mu.RLock()
		_, ok := m.subscribers[in.Subscriber]
		m.mu.RUnlock()
		if !ok {
			return ErrSubscriberNotFound
		}
		q := query.BindParked(tx, m.names)
		scope := q.WithContext(ctx).Where(q.Consumer.Eq(in.Subscriber))
		if in.State != "" {
			scope = scope.Where(q.State.Eq(in.State))
		}
		if in.Reason != "" {
			scope = scope.Where(q.Reason.Eq(in.Reason))
		}
		if in.SubjectKind != "" {
			scope = scope.Where(q.SubjectKind.Eq(adminEqual(in.SubjectKind)))
		}
		var e error
		total, e = scope.Session(&gorm.Session{}).Count()
		if e != nil {
			return e
		}
		rows, e := scope.Session(&gorm.Session{}).Order(q.StreamSeq, q.ID).Offset(in.Offset).Limit(limit).Find()
		if e != nil {
			return e
		}
		for _, r := range rows {
			out = append(out, adminParked(r))
		}
		return nil
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return respx.List(ctx, out, respx.Page{Offset: in.Offset, Limit: limit, Count: len(out), Total: total}), nil
}

type adminParkedMutationInput struct {
	ParkedID string `path:"parked_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch  string `header:"If-Match"`
	Body     struct {
		State  string `json:"state,omitempty" enum:",DISCARDED"`
		Reason string `json:"reason" minLength:"1" maxLength:"2000"`
	}
}
type adminParkedOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[adminParkedView]
}

func (m *Module) mutateAdminParked(ctx context.Context, in *adminParkedMutationInput, retry bool) (*adminParkedOutput, error) {
	if in.IfMatch == "" {
		return nil, respx.ErrStatus(ctx, 428, 171428, eventsi18n.VersionRequired, nil)
	}
	id, err := guid.RequireID(in.ParkedID)
	version, e := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if err != nil || e != nil || version < 1 || strings.TrimSpace(in.Body.Reason) == "" || !utf8.ValidString(in.Body.Reason) || utf8.RuneCountInString(in.Body.Reason) > 2000 || (!retry && in.Body.State != "DISCARDED") {
		return nil, adminHTTPError(ctx, errAdminInvalid)
	}
	var out adminParkedView
	err = m.adminTransaction(ctx, true, func(tx *gorm.DB) error {
		var r *model.Parked
		var e error
		action := "events.parked_retry"
		if retry {
			r, e = m.RetryParkedOn(ctx, tx, id, version, authn.PrincipalIDFromContext(ctx), in.Body.Reason)
		} else {
			action = "events.parked_discard"
			r, e = m.DiscardParkedOn(ctx, tx, id, version, authn.PrincipalIDFromContext(ctx), in.Body.Reason)
		}
		if e != nil {
			return e
		}
		out = adminParked(r)
		return m.adminAuditOn(ctx, tx, action, r.TenantID, "domain_event_parked", id, map[string]any{"reason": in.Body.Reason, "version": r.Version})
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return &adminParkedOutput{ETag: fmt.Sprintf("\"%d\"", out.Version), Body: respx.OK(ctx, out).Body}, nil
}

type adminReplayInput struct {
	AdminSubscriberInput
	Cookie string `header:"Cookie"`
	Key    string `header:"Idempotency-Key" required:"true"`
	Body   struct {
		FromSeq        int64  `json:"from_seq" minimum:"1"`
		ToSeq          *int64 `json:"to_seq,omitempty" minimum:"1"`
		IgnoreReceipts bool   `json:"ignore_receipts"`
		Proof          string `json:"reauth_proof" maxLength:"256"`
	}
}
type adminReplayView struct {
	ReplayCursor string `json:"replay_cursor"`
	FromSeq      int64  `json:"from_seq"`
	ToSeq        int64  `json:"to_seq"`
}

func (m *Module) replayAdminEvents(ctx context.Context, in *adminReplayInput) (*respx.Body[adminReplayView], error) {
	if in.Body.FromSeq < 1 || (in.Body.ToSeq != nil && *in.Body.ToSeq < in.Body.FromSeq) {
		return nil, adminHTTPError(ctx, ErrReplayRange)
	}
	if m.admin.idempotency == nil || m.admin.proof == nil || m.writer() == nil {
		return nil, adminHTTPError(ctx, ErrUnavailable)
	}
	to := int64(0)
	if in.Body.ToSeq != nil {
		to = *in.Body.ToSeq
	}
	digest, err := idempotency.HashRequest(http.MethodPost, "events.replay.create", map[string]string{"subscriber": in.Subscriber}, struct {
		FromSeq        int64  `json:"from_seq"`
		ToSeq          *int64 `json:"to_seq,omitempty"`
		IgnoreReceipts bool   `json:"ignore_receipts"`
	}{in.Body.FromSeq, in.Body.ToSeq, in.Body.IgnoreReceipts})
	if err != nil {
		return nil, adminHTTPError(ctx, errAdminInvalid)
	}
	var out adminReplayView
	var response respx.Envelope[adminReplayView]
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result, _, e := m.admin.idempotency.Do(ctx, tx, idempotency.Key{TenantID: 0, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "events.replay.create", Value: in.Key, RequestHash: digest}, func(tx *gorm.DB) (idempotency.Result, error) {
			if e := m.adminAuthorizeOn(ctx, tx, true); e != nil {
				return idempotency.Result{}, e
			}
			if e := m.admin.proof(ctx, tx, authn.PrincipalIDFromContext(ctx), in.Cookie, in.Body.Proof, replayProofPurpose, in.Subscriber); e != nil {
				return idempotency.Result{}, e
			}
			r, e := m.ReplayOn(ctx, tx, in.Subscriber, in.Body.FromSeq, to, in.Body.IgnoreReceipts)
			if e != nil {
				return idempotency.Result{}, e
			}
			out = adminReplayView(r)
			if e = m.adminAuditOn(ctx, tx, "events.replay_created", 0, "domain_event_subscriber", 0, map[string]any{"subscriber": in.Subscriber, "from_seq": out.FromSeq, "to_seq": out.ToSeq, "ignore_receipts": in.Body.IgnoreReceipts, "replay_cursor": out.ReplayCursor}); e != nil {
				return idempotency.Result{}, e
			}
			body, e := json.Marshal(respx.OK(ctx, out).Body)
			return idempotency.Result{StatusCode: http.StatusCreated, Body: body}, e
		})
		if e != nil {
			return e
		}
		if e = m.adminAuthorizeOn(ctx, tx, true); e != nil {
			return e
		}
		return json.Unmarshal(result.Body, &response)
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return &respx.Body[adminReplayView]{Body: response}, nil
}
