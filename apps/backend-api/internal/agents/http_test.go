package agents

import (
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func agentHTTPHarness(t *testing.T) (*Module, *gorm.DB, http.Handler, huma.API) {
	t.Helper()
	m, db, _ := agentHarness(t)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	respx.Install()
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	return m, db, router, api
}

func TestHTTPRepublishingPreservesSealedOperationManifest(t *testing.T) {
	m, _, _, original := agentHTTPHarness(t)
	manifest := m.registry.List()
	second := humachi.New(chi.NewRouter(), huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), second))
	require.Equal(t, manifest, m.registry.List())
	require.Len(t, second.OpenAPI().Paths, len(original.OpenAPI().Paths))
	require.ErrorIs(t, m.registry.Register(manifest[0]), ErrConflict)
}

func TestHTTPResetHistoryCurrentVisibilityAndBoundedCursor(t *testing.T) {
	m, db, h, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	require.NoError(t, db.Exec("INSERT INTO k_agents_resets (id,tenant_id,agent_principal_id,kind,state,reason,requested_by,created_at) VALUES (10,1,7,'SESSION','TIMED_OUT','Runner did not confirm',2,?)", m.now()).Error)
	path := "/tenants/1/agents/7/resets"
	r := callAgent(t, h, ctx, "GET", path+"?limit=1", "", "")
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), `"state":"TIMED_OUT"`)
	require.Equal(t, 422, callAgent(t, h, ctx, "GET", path+"?cursor=invalid", "", "").Code)
	require.Equal(t, 422, callAgent(t, h, ctx, "GET", path+"?limit=101", "", "").Code)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Equal(t, 403, callAgent(t, h, agentContext(t, 4), "GET", path, "", "").Code)
}

func TestHTTPInboxResumesAckAndFiltersBeforePaging(t *testing.T) {
	m, db, h, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 7)
	for i, kind := range []string{"MESSAGE", "MENTION", "STATE", "MENTION"} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.appendOn(ctx, tx, 1, 7, InboxReference{SourceEventID: fmt.Sprintf("resume:%d", i), Kind: kind, SubjectID: 5})
			return e
		}))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.ackOn(ctx, tx, 1, 7, 1)
		return e
	}))
	read := func(query string) AgentInboxPage {
		t.Helper()
		r := callAgent(t, h, ctx, "GET", "/tenants/1/agents/7/inbox-events"+query, "", "")
		require.Equal(t, 200, r.Code, r.Body.String())
		var response struct {
			Data AgentInboxPage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(r.Body.Bytes(), &response))
		return response.Data
	}
	page := read("")
	require.Len(t, page.Items, 3)
	require.EqualValues(t, 2, page.Items[0].Seq)
	require.EqualValues(t, 1, page.Ack)
	page = read("?since=0")
	require.Len(t, page.Items, 4)
	require.EqualValues(t, 1, page.Items[0].Seq)
	page = read("?kind=MENTION&limit=1")
	require.Len(t, page.Items, 1)
	require.EqualValues(t, 2, page.Items[0].Seq)
	require.True(t, page.HasMore)
	page = read("?kind=MENTION&limit=1&since=2")
	require.EqualValues(t, 4, page.Items[0].Seq)
	require.False(t, page.HasMore)
	for _, query := range []string{"?since=-1", "?since=9223372036854775808", "?since=5", "?kind=UNKNOWN"} {
		r := callAgent(t, h, ctx, "GET", "/tenants/1/agents/7/inbox-events"+query, "", "")
		require.Equal(t, 422, r.Code, r.Body.String())
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.liveOn(ctx, tx, 1, 7)
		require.EqualValues(t, 1, v.Ack, "reads and filters never acknowledge skipped events")
		return e
	}))
	r := callAgent(t, h, agentContext(t, 9), "GET", "/tenants/1/agents/7/inbox-events?kind=MENTION", "", "")
	require.Equal(t, 403, r.Code, r.Body.String())
}
func TestHTTPInboxCriticalReceiptKindsPersistReplayAndFilter(t *testing.T) {
	m, db, h, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 7)
	for i, kind := range []string{"MESSAGE", "DISPATCH", "STATE", "REVIEW_FEEDBACK", "DISPATCH"} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			ref := InboxReference{SourceEventID: fmt.Sprintf("critical:%d", i), Kind: kind, SubjectID: 5, StreamKey: "work_item:5", StreamSeq: int64(i + 1)}
			seq, err := m.appendOn(ctx, tx, 1, 7, ref)
			require.NoError(t, err)
			replay, err := m.appendOn(ctx, tx, 1, 7, ref)
			require.Equal(t, seq, replay)
			return err
		}))
	}
	for _, tc := range []struct {
		query, kind string
		seq         int64
		more        bool
	}{
		{"?kind=DISPATCH&limit=1", "DISPATCH", 2, true},
		{"?kind=DISPATCH&limit=1&since=2", "DISPATCH", 5, false},
		{"?kind=REVIEW_FEEDBACK&limit=1", "REVIEW_FEEDBACK", 4, false},
	} {
		r := callAgent(t, h, ctx, "GET", "/tenants/1/agents/7/inbox-events"+tc.query, "", "")
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
		var response struct {
			Data AgentInboxPage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(r.Body.Bytes(), &response))
		require.Len(t, response.Data.Items, 1)
		require.Equal(t, tc.kind, response.Data.Items[0].Kind)
		require.Equal(t, tc.seq, response.Data.Items[0].Seq)
		require.Equal(t, tc.more, response.Data.HasMore)
		require.EqualValues(t, 5, response.Data.Head)
		require.Zero(t, response.Data.Ack, "filtering critical receipts never ACKs hidden events")
	}
	r := callAgent(t, h, agentContext(t, 9), "GET", "/tenants/1/agents/7/inbox-events?kind=DISPATCH", "", "")
	require.Equal(t, http.StatusForbidden, r.Code, r.Body.String())
}

func callAgent(t *testing.T, handler http.Handler, ctx context.Context, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
func TestHTTPInboxAckReplayAndManifestTransportContracts(t *testing.T) {
	m, db, h, api := agentHTTPHarness(t)
	ctx := agentContext(t, 7)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.appendOn(ctx, tx, 1, 7, InboxReference{SourceEventID: "http:1", Kind: "STATE", SubjectID: 5})
		return e
	}))
	for _, test := range []struct {
		path   string
		status int
	}{{"/tenants/1/agents/7/lifecycle", 200}, {"/tenants/1/agents/7/inbox-events?since=0", 200}, {"/tenants/2/agents/7/inbox-events", 403}, {"/tenants/1/agents/7/inbox-events?since=2", 422}, {"/tenants/1/agents/7/inbox-events?limit=0", 422}, {"/tenants/1/agent-operations", 200}, {"/agents/7/inbox-events", 404}} {
		r := callAgent(t, h, ctx, "GET", test.path, "", "")
		require.Equal(t, test.status, r.Code, r.Body.String())
	}
	path := "/tenants/1/agents/7/inbox-acks"
	r := callAgent(t, h, ctx, "POST", path, "", `{"since":1}`)
	require.Equal(t, 422, r.Code, r.Body.String())
	r = callAgent(t, h, ctx, "POST", path, "ack-1", `{"since":1}`)
	require.Equal(t, 201, r.Code, r.Body.String())
	r = callAgent(t, h, ctx, "POST", path, "ack-1", `{"since":1}`)
	require.Equal(t, 201, r.Code, r.Body.String())
	require.Equal(t, "true", r.Header().Get("Idempotent-Replayed"))
	r = callAgent(t, h, ctx, "POST", path, "ack-1", `{"since":0}`)
	require.Equal(t, 409, r.Code, r.Body.String())
	r = callAgent(t, h, agentContext(t, 2), "POST", path, "human-ack", `{"since":1}`)
	require.Equal(t, 403, r.Code, r.Body.String())
	// The manifest must independently describe actual, registered endpoints.
	operations := m.registry.List()
	names := map[string]bool{}
	for _, op := range operations {
		names[op.Name] = true
		require.Contains(t, api.OpenAPI().Paths, op.Path)
		require.True(t, strings.HasPrefix(op.Path, "/tenants/{tenant_id}/"))
		require.NotContains(t, string(op.InputSchema), `"$ref"`)
		require.NotContains(t, string(op.OutputSchema), `"$ref"`)
		if op.IdempotencyRequired {
			found := false
			for _, p := range op.Parameters {
				if p.Name == "Idempotency-Key" {
					found = true
					require.True(t, p.Required)
				}
			}
			require.True(t, found)
		}
	}
	require.Equal(t, map[string]bool{
		"agent_capabilities.get":       true,
		"agent_manifest.get":           true,
		"agent_manifest.versions":      true,
		"agent_operations.list":        true,
		"agent_protocol.live":          true,
		"agent_protocol.reset":         true,
		"agent_protocol.reset_history": true,
		"agent_reminders.change":       true,
		"agent_reminders.create":       true,
		"agent_reminders.list":         true,
		"agent_tools.create":           true,
		"agent_tools.detail":           true,
		"agent_tools.grant":            true,
		"agent_tools.list":             true,
		"agent_tools.metadata":         true,
		"agent_tools.presets":          true,
		"agent_tools.publish":          true,
		"agent_tools.sources":          true,
		"agent_tools.state":            true,
		"agent_tools.upstream":         true,
		"agent_tools.versions":         true,
		"inbox_events.ack":             true,
		"inbox_events.list":            true,
	}, names)
	for _, next := range []string{"agent_protocol.live", "agent_tools.grant", "agent_tools.list", "agent_reminders.create", "inbox_events.ack"} {
		require.True(t, names[next], next)
	}
	for _, op := range operations {
		if op.Name == "inbox_events.ack" {
			var schema map[string]any
			require.NoError(t, json.Unmarshal(op.InputSchema, &schema))
			require.Contains(t, schema["properties"], "since")
			require.NotContains(t, schema["properties"], "Body")
		}
	}
	require.ErrorIs(t, m.registry.Register(operations[0]), ErrConflict)
}
func TestHTTPReminderReplayRechecksThreadAccess(t *testing.T) {
	m, db, h, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 7)
	m.now = func() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) }
	body := `{"project_id":"3","thread_id":"4","title":"Review","note":"Private note","due_at":"2026-10-21T12:00:00Z","interval_minutes":0,"max_fires":1,"checks":"","budget_minor":0,"escalation":"","output_format":""}`
	path := "/tenants/1/agents/7/reminders"
	r := callAgent(t, h, ctx, "POST", path, "reminder-1", body)
	require.Equal(t, 201, r.Code, r.Body.String())
	var envelope respx.Envelope[ReminderView]
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &envelope))
	id := envelope.Data.ID
	r = callAgent(t, h, ctx, "POST", path, "reminder-1", body)
	require.Equal(t, 201, r.Code)
	require.Equal(t, "true", r.Header().Get("Idempotent-Replayed"))
	r = callAgent(t, h, ctx, "GET", path, "", "")
	require.Equal(t, 200, r.Code)
	require.Contains(t, r.Body.String(), "Private note")
	change := path + "/" + id.String() + "/state"
	r = callAgent(t, h, ctx, "POST", change, "pause-1", `{"state":"PAUSED","version":1}`)
	require.Equal(t, 201, r.Code, r.Body.String())
	m.threads.CheckOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (int64, error) {
		return 0, ErrForbidden
	}
	for _, test := range []struct{ method, path, key, body string }{{"POST", path, "reminder-1", body}, {"POST", change, "pause-1", `{"state":"PAUSED","version":1}`}, {"POST", change, "resume-2", `{"state":"ACTIVE","version":2}`}, {"GET", path, "", ""}} {
		r = callAgent(t, h, ctx, test.method, test.path, test.key, test.body)
		require.Equal(t, 403, r.Code, r.Body.String())
		require.NotContains(t, r.Body.String(), "Private note")
	}
	q := query.BindReminder(db, m.names)
	row, e := q.WithContext(ctx).First()
	require.NoError(t, e)
	require.Equal(t, "PAUSED", row.State)
	require.Equal(t, 2, row.Version)
}
func TestHTTPToolsAndResetFailureAreTruthful(t *testing.T) {
	m, _, h, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	body := `{"entry_key":"team-skill","kind":"SKILL","title":"Evidence","transport":"","endpoint":"","content_hash":"","skill_body":"Cite verified evidence"}`
	r := callAgent(t, h, ctx, "POST", "/tenants/1/agent-tools", "tool-1", body)
	require.Equal(t, 201, r.Code, r.Body.String())
	var env respx.Envelope[ToolView]
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &env))
	id := env.Data.ID
	r = callAgent(t, h, ctx, "POST", "/tenants/1/agent-tools", "tool-1", body)
	require.Equal(t, 201, r.Code)
	require.Equal(t, "true", r.Header().Get("Idempotent-Replayed"))
	r = callAgent(t, h, ctx, "POST", "/tenants/1/agent-tools/"+id.String()+"/grants", "grant-1", `{"agent_principal_id":"7","state":"ACTIVE"}`)
	require.Equal(t, 201, r.Code, r.Body.String())
	r = callAgent(t, h, agentContext(t, 7), "GET", "/tenants/1/agent-tools?agent_principal_id=7", "", "")
	require.Equal(t, 200, r.Code)
	require.Contains(t, r.Body.String(), "team-skill")
	r = callAgent(t, h, ctx, "POST", "/tenants/1/agents/7/resets", "reset-1", `{"kind":"SESSION","reason":"Clear the stale session"}`)
	require.Equal(t, 503, r.Code, r.Body.String())
	require.NoError(t, m.bindRuntime(RuntimeControl{RequestOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, int, int) error { return nil }}))
	r = callAgent(t, h, ctx, "POST", "/tenants/1/agents/7/resets", "reset-1", `{"kind":"SESSION","reason":"Clear the stale session"}`)
	require.Equal(t, 202, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), "REQUESTED")
	require.NotContains(t, r.Body.String(), `"state":"DONE"`)
	r = callAgent(t, h, ctx, "POST", "/tenants/1/agents/7/resets", "reset-1", `{"kind":"SESSION","reason":"Clear the stale session"}`)
	require.Equal(t, 202, r.Code)
	require.Equal(t, "true", r.Header().Get("Idempotent-Replayed"))
}
func TestAgentErrorsAndInvalidHTTPInputs(t *testing.T) {
	m, _, h, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	for _, tc := range []struct {
		err    error
		status int
	}{{ErrInvalid, 422}, {ErrForbidden, 403}, {ErrNotFound, 404}, {ErrConflict, 409}, {ErrUnavailable, 503}, {idempotency.ErrInvalidKey, 422}, {idempotency.ErrKeyReused, 409}, {idempotency.ErrInProgress, 409}, {gorm.ErrDuplicatedKey, 409}, {gorm.ErrRecordNotFound, 404}, {errors.New("private secret SQL"), 500}} {
		err := agentHTTPError(ctx, tc.err)
		var status huma.StatusError
		require.ErrorAs(t, err, &status)
		require.Equal(t, tc.status, status.GetStatus())
		require.NotContains(t, err.Error(), "private secret")
	}
	for _, path := range []string{"/tenants/2/agent-operations", "/tenants/2/agent-tools", "/tenants/1/agent-tools?cursor=bad", "/tenants/1/agent-tools?agent_principal_id=bad", "/tenants/1/agents/7/reminders?cursor=bad", "/tenants/1/agents/bad/lifecycle"} {
		r := callAgent(t, h, ctx, "GET", path, "", "")
		require.GreaterOrEqual(t, r.Code, 400, r.Body.String())
	}
	m.runtime = nil
	r := callAgent(t, h, ctx, "GET", "/tenants/1/agents/7/lifecycle", "", "")
	require.Equal(t, 503, r.Code)
	_, e := position("invalid")
	require.ErrorIs(t, e, ErrInvalid)
	_, _, e = pathIDs(ctx, AgentPath{TenantID: "bad", AgentPrincipalID: "7"})
	require.ErrorIs(t, e, ErrInvalid)
}

func TestHTTPForeignTenantCommandsNeverMutateOwnedTables(t *testing.T) {
	_, db, h, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	for _, tc := range []struct{ path, body string }{
		{"/tenants/2/agents/7/inbox-acks", `{"since":0}`},
		{"/tenants/2/agents/7/resets", `{"kind":"SESSION","reason":"Clear stale session state"}`},
		{"/tenants/2/agents/7/reminders", `{"project_id":"3","thread_id":"4","title":"Review","note":"","due_at":"2040-01-01T00:00:00Z","interval_minutes":0,"max_fires":1,"checks":"","budget_minor":0,"escalation":"","output_format":""}`},
		{"/tenants/2/agents/7/reminders/1/state", `{"state":"PAUSED","version":1}`},
		{"/tenants/2/agent-tools", `{"entry_key":"foreign-skill","kind":"SKILL","title":"Skill","transport":"","endpoint":"","content_hash":"","skill_body":"Instructions"}`},
		{"/tenants/2/agent-tools/1/grants", `{"agent_principal_id":"7","state":"ACTIVE"}`},
	} {
		r := callAgent(t, h, ctx, "POST", tc.path, "foreign-action", tc.body)
		require.Equal(t, 403, r.Code, "%s: %s", tc.path, r.Body.String())
		var body map[string]any
		require.NoError(t, json.Unmarshal(r.Body.Bytes(), &body))
		require.Contains(t, body["meta"], "next")
	}
	for _, table := range []string{"k_agents_live_states", "k_agents_resets", "k_agents_reminders", "k_agents_tool_entries", "k_agents_tool_grants"} {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		require.Zero(t, n)
	}
}

func TestProtocolHTTPPaginationAndInvalidDomainPathsNeverLeakForeignRows(t *testing.T) {
	m, db, h, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	for _, key := range []string{"page-one", "page-two"} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: key, Kind: "SKILL", Title: key, SkillBody: "Use actual sources"})
			return e
		}))
	}
	tools := callAgent(t, h, ctx, "GET", "/tenants/1/agent-tools?limit=1", "", "")
	require.Equal(t, 200, tools.Code, tools.Body.String())
	require.Contains(t, tools.Body.String(), `"has_more":true`)
	require.Contains(t, tools.Body.String(), `"next_cursor":`)
	for _, title := range []string{"first", "second"} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.reminderOn(agentContext(t, 7), tx, 1, 7, ReminderInput{ProjectID: new(guid.ID(3)), ThreadID: 4, Title: title, DueAt: m.now().Add(time.Hour), MaxFires: 1})
			return e
		}))
	}
	reminders := callAgent(t, h, agentContext(t, 7), "GET", "/tenants/1/agents/7/reminders?limit=1", "", "")
	require.Equal(t, 200, reminders.Code, reminders.Body.String())
	require.Contains(t, reminders.Body.String(), `"has_more":true`)
	require.Contains(t, reminders.Body.String(), `"next_cursor":`)
	bad := AgentPath{TenantID: "1", AgentPrincipalID: "bad"}
	_, err := m.liveHTTP(ctx, &bad)
	require.Error(t, err)
	_, err = m.resetHistoryHTTP(ctx, &ReminderListInput{AgentPath: bad})
	require.Error(t, err)
	_, err = m.remindersHTTP(ctx, &ReminderListInput{AgentPath: bad})
	require.Error(t, err)
	_, err = m.reminderStateHTTP(ctx, &ReminderStateInput{AgentPath: bad, ReminderID: "bad"})
	require.Error(t, err)
	_, err = m.grantHTTP(ctx, &ToolGrantInput{TeamPath: TeamPath{TenantID: "1"}, ToolID: "bad"})
	require.Error(t, err)
}
