package agents

import (
	agentsi18n "7link.com/platform/internal/agents/i18n"
	"7link.com/platform/internal/agents/model"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

type AgentPath struct {
	TenantID         string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	AgentPrincipalID string `path:"agent_principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type TeamPath struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type InboxInput struct {
	ContextInput
	AgentPath
	Since string `query:"since" pattern:"^[0-9]+$" maxLength:"19" doc:"Omit to resume the persisted ACK; explicit zero replays from the start."`
	Kind  string `query:"kind" enum:"MESSAGE,MENTION,REMINDER,STATE,EXECUTION,INTERACTION,DISPATCH,REVIEW_FEEDBACK"`
	Limit int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
}
type AckInput struct {
	AgentPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Since int64 `json:"since" minimum:"0"`
	}
}
type ResetHTTPInput struct {
	Cookie string `header:"Cookie"`
	AgentPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           ResetInput
}
type ReminderHTTPInput struct {
	AgentPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           ReminderInput
}
type ReminderListInput struct {
	AgentPath
	Cursor string `query:"cursor" maxLength:"19"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
}
type ReminderStateInput struct {
	AgentPath
	ReminderID     string `path:"reminder_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		State   string `json:"state" enum:"ACTIVE,PAUSED,CANCELLED"` //nolint:misspell // Spec 05 ReminderState wire spelling.
		Version int    `json:"version" minimum:"1"`
	}
}
type ToolHTTPInput struct {
	Cookie string `header:"Cookie"`
	TeamPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           ToolInput
}
type ToolListInput struct {
	TeamPath
	AgentPrincipalID string `query:"agent_principal_id"`
	Cursor           string `query:"cursor" maxLength:"19"`
	Limit            int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
}
type ToolGrantInput struct {
	TeamPath
	ToolID         string `path:"tool_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		AgentPrincipalID guid.ID `json:"agent_principal_id"`
		State            string  `json:"state" enum:"ACTIVE,REVOKED"`
	}
}
type Page[T any] struct {
	Items      []T    `json:"items"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
	Next       []Next `json:"next"`
}
type Manifest struct {
	Knowledge  []knowledge.ContextItem `json:"knowledge,omitempty"`
	Operations []Operation             `json:"operations"`
	Next       []Next                  `json:"next"`
}
type CommandOutput[T any] struct {
	Replayed string `header:"Idempotent-Replayed"`
	Body     ProtocolEnvelope[T]
}

// Protocol metadata extends the shared envelope without changing its fields.
type ProtocolMeta struct {
	respx.Meta
	Next []Next `json:"next"`
}
type ProtocolEnvelope[T any] struct {
	Code    int          `json:"code"`
	Message string       `json:"message"`
	Meta    ProtocolMeta `json:"meta"`
	Data    T            `json:"data"`
}
type ProtocolBody[T any] struct{ Body ProtocolEnvelope[T] }

func protocolOK[T any](ctx context.Context, value T) *ProtocolBody[T] {
	base := respx.OK(ctx, value).Body
	next := []Next{}
	if provider, ok := any(value).(interface{ NextSteps() []Next }); ok {
		next = provider.NextSteps()
	}
	return &ProtocolBody[T]{Body: ProtocolEnvelope[T]{Code: base.Code, Message: platformi18n.TContext(ctx, base.Message), Meta: ProtocolMeta{Meta: base.Meta, Next: next}, Data: value}}
}
func (v LiveView) NextSteps() []Next       { return v.Next }
func (v AgentInboxPage) NextSteps() []Next { return v.Next }
func (v ResetView) NextSteps() []Next      { return v.Next }
func (v ReminderView) NextSteps() []Next   { return v.Next }
func (v ToolView) NextSteps() []Next       { return v.Next }
func (v GrantView) NextSteps() []Next      { return v.Next }
func (v Manifest) NextSteps() []Next       { return v.Next }
func (v Page[T]) NextSteps() []Next        { return v.Next }

func (m *Module) database() (*gorm.DB, error) {
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, ErrUnavailable
	}
	return m.runtime.Database().Write(), nil
}

type protocolError struct {
	huma.StatusError
	value ProtocolEnvelope[any]
}

func (e *protocolError) MarshalJSON() ([]byte, error) { return json.Marshal(e.value) }
func agentHTTPError(ctx context.Context, err error) error {
	base := agentBaseError(ctx, err)
	raw, e := respx.MarshalEnvelope(ctx, base)
	if e != nil {
		return base
	}
	var value ProtocolEnvelope[any]
	if json.Unmarshal(raw, &value) != nil {
		return base
	}
	value.Meta.Next = []Next{}
	var status huma.StatusError
	if !errors.As(base, &status) {
		return base
	}
	return &protocolError{StatusError: status, value: value}
}
func agentBaseError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ErrRetirementNameMismatch):
		return respx.ErrStatus(ctx, 422, 142016, agentsi18n.RetirementNameMismatch, nil)
	case errors.Is(err, ErrInvalid), errors.Is(err, idempotency.ErrInvalidKey):
		return respx.ErrStatus(ctx, 422, 142422, agentsi18n.Invalid, nil)
	case errors.Is(err, ErrForbidden):
		return respx.ErrStatus(ctx, 403, 142403, agentsi18n.Forbidden, nil)
	case errors.Is(err, ErrNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		return respx.ErrStatus(ctx, 404, 142404, agentsi18n.NotFound, nil)
	case errors.Is(err, ErrConflict), errors.Is(err, idempotency.ErrKeyReused), errors.Is(err, idempotency.ErrInProgress), errors.Is(err, gorm.ErrDuplicatedKey):
		return respx.ErrStatus(ctx, 409, 142409, agentsi18n.Conflict, nil)
	case errors.Is(err, ErrUnavailable):
		return respx.ErrStatus(ctx, 503, 142503, agentsi18n.Unavailable, nil)
	default:
		return respx.ErrStatus(ctx, 500, 142500, respx.InternalServerError, nil)
	}
}
func pathIDs(ctx context.Context, in AgentPath) (guid.ID, guid.ID, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return 0, 0, ErrInvalid
	}
	agent, e := guid.RequireID(in.AgentPrincipalID)
	if e != nil {
		return 0, 0, ErrInvalid
	}
	if tenant != tenantID(ctx) {
		return 0, 0, ErrForbidden
	}
	return tenant, agent, nil
}
func readHTTP[T any](ctx context.Context, m *Module, fn func(*gorm.DB) (T, error)) (*ProtocolBody[T], error) {
	db, err := m.database()
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	var out T
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = fn(tx); return e })
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	return protocolOK(ctx, out), nil
}
func agentCommand[T any](ctx context.Context, m *Module, key, operation, path string, params map[string]string, body any, check func(*gorm.DB) error, fn func(*gorm.DB) (T, error)) (*CommandOutput[T], error) {
	db, err := m.database()
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	if m.replay == nil {
		return nil, agentHTTPError(ctx, ErrUnavailable)
	}
	hash, err := idempotency.HashRequest(http.MethodPost, path, params, body)
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	k := idempotency.Key{TenantID: tenantID(ctx), SubjectKind: "PRINCIPAL", SubjectID: actorID(ctx), Operation: operation, Value: key, RequestHash: hash}
	if err = k.Validate(); err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	var value T
	var replayed bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Current authority is checked even when returning a saved result.
		if err := check(tx); err != nil {
			return err
		}
		result, replay, e := m.replay.Do(ctx, tx, k, func(tx *gorm.DB) (idempotency.Result, error) {
			v, e := fn(tx)
			if e != nil {
				return idempotency.Result{}, e
			}
			encoded, e := json.Marshal(v)
			return idempotency.Result{StatusCode: 201, Body: encoded}, e
		})
		if e != nil {
			return e
		}
		replayed = replay
		if err := json.Unmarshal(result.Body, &value); err != nil {
			return err
		}
		if replayed {
			if retirement, ok := any(value).(model.Retirement); ok {
				return m.checkRetirementSourceReadsOn(ctx, tx, &retirement)
			}
		}
		return nil
	})
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	out := &CommandOutput[T]{Body: protocolOK(ctx, value).Body}
	if replayed {
		out.Replayed = "true"
	}
	return out, nil
}

const agentPath = "/agents/{agent_principal_id}"

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	iamauthn.RegisterAgentsReauthentication(api)
	if m.registrar == nil {
		return ErrUnavailable
	}
	m.registerDirectMessagesHTTP(api)
	m.registerProfileHTTP(api)
	m.registerPerformanceHTTP(api)
	if err := m.registerRetirementHTTP(api); err != nil {
		return err
	}
	if err := m.registerCreationHTTP(api); err != nil {
		return err
	}
	m.registerProcessHTTP(api)
	m.registerRosterHTTP(api)
	if err := m.registerToolRelayHTTP(api); err != nil {
		return err
	}
	if err := m.registerWorkspaceHTTP(api); err != nil {
		return err
	}
	m.registerWakeHTTP(api)
	m.registerManifestHTTP(api)
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	// SELF and owner-relation paths use membership transport guards. Domain
	// methods always re-evaluate the exact current principal and IAM relation.
	authz.RegisterTenantMember(m.registrar, api, op("agent_protocol.live", http.MethodGet, agentPath+"/lifecycle", "Read observed agent lifecycle and inbox positions", 200), m.liveHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("inbox_events.list", http.MethodGet, agentPath+"/inbox-events", "Read durable agent inbox references since a cursor", 200), m.inboxHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("inbox_events.ack", http.MethodPost, agentPath+"/inbox-acks", "Acknowledge own inbox monotonically", 201), m.ackHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_protocol.reset", http.MethodPost, agentPath+"/resets", "Request a runner-confirmed reset", 202), m.resetHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_protocol.reset_history", http.MethodGet, agentPath+"/resets", "Read authorized reset requests and persisted outcomes", 200), m.resetHistoryHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_reminders.list", http.MethodGet, agentPath+"/reminders", "List scoped reminders and loop contracts", 200), m.remindersHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_reminders.create", http.MethodPost, agentPath+"/reminders", "Create an anchored reminder", 201), m.reminderHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_reminders.change", http.MethodPost, agentPath+"/reminders/{reminder_id}/state", "Pause resume or cancel a reminder", 201), m.reminderStateHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_operations.list", http.MethodGet, "/agent-operations", "Read the registered agent operation contracts", 200), m.manifestHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_tools.list", http.MethodGet, "/agent-tools", "List a team catalog or granted tools for an agent", 200), m.toolsHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_tools.create", http.MethodPost, "/agent-tools", "Create an MCP server or a versioned skill entry", 201), m.toolHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_tools.grant", http.MethodPost, "/agent-tools/{tool_id}/grants", "Grant or revoke an agent tool", 201), m.grantHTTP)
	if err := m.registerToolVersionsHTTP(api); err != nil {
		return err
	}
	m.publishProtocolErrors(api)
	if err := m.registerOperations(api); err != nil {
		return err
	}
	if err := m.sealOperations(); err != nil {
		return err
	}
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	for _, path := range []string{agentPath + "/wake-config", agentPath + "/inbox-acks", agentPath + "/resets", agentPath + "/reminders", agentPath + "/reminders/{reminder_id}/state", "/agent-tools", "/agent-tools/{tool_id}/grants"} {
		if err := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+path); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) liveHTTP(ctx context.Context, in *AgentPath) (*ProtocolBody[LiveView], error) {
	t, a, e := pathIDs(ctx, *in)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (LiveView, error) { return m.liveOn(ctx, tx, t, a) })
}
func (m *Module) inboxHTTP(ctx context.Context, in *InboxInput) (*ProtocolBody[AgentInboxPage], error) {
	t, a, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	var since *int64
	if in.Since != "" {
		value, err := strconv.ParseInt(in.Since, 10, 64)
		if err != nil || value < 0 {
			return nil, agentHTTPError(ctx, ErrInvalid)
		}
		since = &value
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (AgentInboxPage, error) {
		items, err := m.contextOn(ctx, tx, t, a, in.ExecutionID)
		if err != nil {
			return AgentInboxPage{}, err
		}
		page, err := m.filteredInboxOn(ctx, tx, t, a, since, in.Limit, in.Kind)
		page.Knowledge = items
		return page, err
	})
}
func agentParams(in AgentPath) map[string]string {
	return map[string]string{"tenant_id": in.TenantID, "agent_principal_id": in.AgentPrincipalID}
}
func (m *Module) ackHTTP(ctx context.Context, in *AckInput) (*CommandOutput[LiveView], error) {
	t, a, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return agentCommand(ctx, m, in.IdempotencyKey, "inbox_events.ack", agentPath+"/inbox-acks", agentParams(in.AgentPath), in.Body, func(tx *gorm.DB) error { _, e := m.authorize(ctx, tx, t, a, "SELF", false); return e }, func(tx *gorm.DB) (LiveView, error) { return m.ackOn(ctx, tx, t, a, in.Body.Since) })
}
func (m *Module) resetHTTP(ctx context.Context, in *ResetHTTPInput) (*CommandOutput[ResetView], error) {
	ctx = context.WithValue(ctx, proofCookieKey{}, in.Cookie)
	t, a, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	body := in.Body
	body.ReauthProof = ""
	return agentCommand(ctx, m, in.IdempotencyKey, "agent_protocol.reset", agentPath+"/resets", agentParams(in.AgentPath), body, func(tx *gorm.DB) error { _, e := m.authorize(ctx, tx, t, a, "agent_manage", true); return e }, func(tx *gorm.DB) (ResetView, error) { return m.resetOn(ctx, tx, t, a, in.Body) })
}
func position(s string) (guid.ID, error) {
	if s == "" {
		return 0, nil
	}
	id, e := guid.RequireID(s)
	if e != nil {
		return 0, ErrInvalid
	}
	return id, nil
}

func (m *Module) resetHistoryHTTP(ctx context.Context, in *ReminderListInput) (*ProtocolBody[Page[ResetHistoryView]], error) {
	t, a, err := pathIDs(ctx, in.AgentPath)
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	before, err := position(in.Cursor)
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (Page[ResetHistoryView], error) {
		return m.resetHistoryOn(ctx, tx, t, a, before, in.Limit)
	})
}
func (m *Module) remindersHTTP(ctx context.Context, in *ReminderListInput) (*ProtocolBody[Page[ReminderView]], error) {
	t, a, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	after, e := position(in.Cursor)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (Page[ReminderView], error) {
		rows, more, e := m.remindersOn(ctx, tx, t, a, after, in.Limit)
		out := Page[ReminderView]{Items: rows, HasMore: more, Next: []Next{{Operation: "agent_reminders.create"}}}
		if more {
			out.NextCursor = rows[len(rows)-1].ID.String()
		}
		return out, e
	})
}
func (m *Module) reminderHTTP(ctx context.Context, in *ReminderHTTPInput) (*CommandOutput[ReminderView], error) {
	t, a, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return agentCommand(ctx, m, in.IdempotencyKey, "agent_reminders.create", agentPath+"/reminders", agentParams(in.AgentPath), in.Body, func(tx *gorm.DB) error {
		_, e := m.authorize(ctx, tx, t, a, m.reminderPermission(ctx, a), false)
		if e != nil {
			return e
		}
		return m.checkReminderInputAccess(ctx, tx, t, a, in.Body)
	}, func(tx *gorm.DB) (ReminderView, error) { return m.reminderOn(ctx, tx, t, a, in.Body) })
}
func (m *Module) reminderStateHTTP(ctx context.Context, in *ReminderStateInput) (*CommandOutput[ReminderView], error) {
	t, a, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	id, e := guid.RequireID(in.ReminderID)
	if e != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	params := agentParams(in.AgentPath)
	params["reminder_id"] = in.ReminderID
	return agentCommand(ctx, m, in.IdempotencyKey, "agent_reminders.change", agentPath+"/reminders/{reminder_id}/state", params, in.Body, func(tx *gorm.DB) error {
		_, e := m.authorize(ctx, tx, t, a, m.reminderPermission(ctx, a), false)
		if e != nil {
			return e
		}
		return m.checkReminderAccess(ctx, tx, t, a, id)
	}, func(tx *gorm.DB) (ReminderView, error) {
		return m.changeReminderOn(ctx, tx, t, a, id, in.Body.Version, in.Body.State)
	})
}
func (m *Module) toolsHTTP(ctx context.Context, in *ToolListInput) (*ProtocolBody[Page[ToolView]], error) {
	if in.TenantID != tenantID(ctx).String() {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	a, e := position(in.AgentPrincipalID)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	after, e := position(in.Cursor)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (Page[ToolView], error) {
		rows, more, e := m.toolsOn(ctx, tx, tenantID(ctx), a, after, in.Limit)
		out := Page[ToolView]{Items: rows, HasMore: more, Next: []Next{{Operation: "agent_tools.grant"}}}
		if more {
			out.NextCursor = rows[len(rows)-1].ID.String()
		}
		return out, e
	})
}
func (m *Module) toolHTTP(ctx context.Context, in *ToolHTTPInput) (*CommandOutput[ToolView], error) {
	ctx = context.WithValue(ctx, proofCookieKey{}, in.Cookie)
	if in.TenantID != tenantID(ctx).String() {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	body := in.Body
	body.ReauthProof = ""
	return agentCommand(ctx, m, in.IdempotencyKey, "agent_tools.create", "/agent-tools", map[string]string{"tenant_id": in.TenantID}, body, func(tx *gorm.DB) error {
		if err := m.teamToolAuthority(ctx, tx, tenantID(ctx)); err != nil {
			return err
		}
		candidate := in.Body
		_, _, err := m.prepareToolSourceOn(ctx, tx, tenantID(ctx), &candidate)
		return err
	}, func(tx *gorm.DB) (ToolView, error) { return m.createToolOn(ctx, tx, tenantID(ctx), in.Body) })
}
func (m *Module) grantHTTP(ctx context.Context, in *ToolGrantInput) (*CommandOutput[GrantView], error) {
	if in.TenantID != tenantID(ctx).String() {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	id, e := guid.RequireID(in.ToolID)
	if e != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	return agentCommand(ctx, m, in.IdempotencyKey, "agent_tools.grant", "/agent-tools/{tool_id}/grants", map[string]string{"tenant_id": in.TenantID, "tool_id": in.ToolID}, in.Body, func(tx *gorm.DB) error { return m.teamToolAuthority(ctx, tx, tenantID(ctx)) }, func(tx *gorm.DB) (GrantView, error) {
		return m.grantOn(ctx, tx, tenantID(ctx), id, in.Body.AgentPrincipalID, in.Body.State)
	})
}

var _ module.HTTPRegistrar = (*Module)(nil)

type AgentProtocolErrorEnvelope ProtocolEnvelope[any]

// Domain errors carry next, while transport/auth/validation errors retain the
// shared envelope. Publish both contracts rather than widening shared metadata.
func (m *Module) publishProtocolErrors(api huma.API) {
	doc := api.OpenAPI()
	protocol := doc.Components.Schemas.Schema(reflect.TypeFor[AgentProtocolErrorEnvelope](), true, "AgentProtocolErrorEnvelope")
	for _, item := range doc.Paths {
		for _, op := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete} {
			if op == nil || !(strings.HasPrefix(op.OperationID, "agent_") || strings.HasPrefix(op.OperationID, "inbox_events.")) {
				continue
			}
			for status, response := range op.Responses {
				if len(status) != 3 || status[0] < '4' {
					continue
				}
				media := response.Content["application/json"]
				if media == nil {
					continue
				}
				copyResponse := *response
				copyMedia := *media
				copyMedia.Schema = &huma.Schema{AnyOf: []*huma.Schema{media.Schema, protocol}}
				copyResponse.Content = map[string]*huma.MediaType{"application/json": &copyMedia}
				op.Responses[status] = &copyResponse
			}
		}
	}
}
