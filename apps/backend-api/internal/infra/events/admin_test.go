package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/events/model"
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type adminAuthority struct{ current bool }

func (a *adminAuthority) Authenticate(_ context.Context, authorization, _ string) (*authn.Claims, error) {
	id := guid.ID(1)
	if authorization == "Bearer viewer" {
		id = 2
	} else if authorization == "Bearer tenant" {
		id = 3
	} else if authorization != "Bearer manager" {
		return nil, authn.ErrInvalidCredentials
	}
	return &authn.Claims{PrincipalID: id, SubjectType: authn.SubjectTypePrincipal}, nil
}
func (a *adminAuthority) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return false, nil
}
func (a *adminAuthority) CheckPlatform(_ context.Context, p string, id guid.ID) (bool, error) {
	return id == 1 || (id == 2 && p == "system_event_view"), nil
}
func (a *adminAuthority) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

type adminFixture struct {
	m                  *Module
	db                 *gorm.DB
	api                humatest.TestAPI
	authority          *adminAuthority
	eventID            guid.ID
	proofCalls, audits int
	auditError         error
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	require.NoError(t, platformi18n.NewModule(nil, "en-US").Initialize(t.Context()))
	m, db := newTestSequencer(t)
	f := &adminFixture{m: m, db: db, authority: &adminAuthority{current: true}}
	m.mu.Lock()
	m.sealed = false
	m.mu.Unlock()
	require.NoError(t, m.AddSubscribers("delivery", Subscriber{Name: "delivery.admin", EventTypes: []string{"project.created"}, Mode: Transactional, StartFrom: Earliest, Handle: func(context.Context, *gorm.DB, Envelope) error { return nil }}))
	m.mu.Lock()
	m.sealed = true
	m.mu.Unlock()
	var err error
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		e := testEvent()
		e.Data = map[string]any{"access_token": "private-token", "summary": "test detail"}
		f.eventID, err = m.Publish(t.Context(), tx, e)
		return err
	}))
	_, err = m.Sequence(t.Context())
	require.NoError(t, err)
	idem := idempotency.NewModule(m.runtime)
	s := module.NewServices()
	require.NoError(t, idem.ProvideServices(s))
	require.NoError(t, module.Provide[guid.Source](s, m.ids))
	require.NoError(t, m.provideAdmin(s))
	require.NoError(t, s.Seal())
	require.NoError(t, idem.InjectServices(s))
	require.NoError(t, idem.Initialize(t.Context()))
	raw, err := db.DB()
	require.NoError(t, err)
	source := idem.MigrationSource()
	source.Prefix = "ev_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	c, err := module.Resolve[AdminConnector](s)
	require.NoError(t, err)
	r := authz.NewRegistrar(authz.MustRegistry(), f.authority, f.authority, f.authority)
	require.NoError(t, c.Bind(r, func(ctx context.Context, tx *gorm.DB, p string, id guid.ID) (bool, error) {
		require.NotNil(t, tx)
		if !f.authority.current {
			return false, nil
		}
		return f.authority.CheckPlatform(ctx, p, id)
	}))
	require.NoError(t, c.BindAudit(func(_ context.Context, tx *gorm.DB, _ string, _ guid.ID, _ string, _ guid.ID, _ map[string]any) error {
		require.NotNil(t, tx)
		f.audits++
		return f.auditError
	}))
	require.NoError(t, db.Exec("CREATE TABLE admin_proof_probe (id INTEGER PRIMARY KEY, consumed INTEGER NOT NULL)").Error)
	require.NoError(t, db.Exec("INSERT INTO admin_proof_probe VALUES (1,0)").Error)
	require.NoError(t, c.BindSecurity(func(_ context.Context, tx *gorm.DB, id guid.ID, cookie, proof, purpose, target string) error {
		f.proofCalls++
		if id != 1 || cookie != "browser=valid" || proof != "fresh" || purpose != replayProofPurpose || target != "delivery.admin" {
			return authn.ErrInvalidCredentials
		}
		result := tx.Exec("UPDATE admin_proof_probe SET consumed=1 WHERE id=1 AND consumed=0")
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return authn.ErrInvalidCredentials
		}
		return nil
	}, idem))
	require.NoError(t, m.Initialize(t.Context()))
	_, f.api = humatest.New(t)
	require.NoError(t, m.RegisterHTTP(t.Context(), f.api))
	return f
}
func (f *adminFixture) park(t *testing.T, reason string, seq int64) *model.Parked {
	t.Helper()
	id, err := f.m.newID()
	require.NoError(t, err)
	r := &model.Parked{ID: id, Consumer: "delivery.admin", PartitionNo: 0, EventID: guid.ID(1000 + seq), StreamSeq: seq, TenantID: 1, SubjectKind: "project", SubjectID: guid.ID(seq), Reason: reason, State: "PARKED", CreatedAt: f.m.now(), UpdatedAt: f.m.now(), Version: 1}
	require.NoError(t, query.BindParked(f.db, f.m.names).WithContext(t.Context()).Create(r))
	return r
}
func TestEventsAdminReadAuthorityFilteringAndSensitiveAudit(t *testing.T) {
	f := newAdminFixture(t)
	path := "/platform/domain-events/" + f.eventID.String()
	out := f.api.Get("/platform/domain-events", "Authorization: Bearer tenant")
	require.Equal(t, http.StatusForbidden, out.Code)
	out = f.api.Get("/platform/domain-events?event_type=eq:project.created&tenant_id=eq:1&subject_kind=eq:project&subject_id=eq:2&stream_seq=gt:0&occurred_at=gte:2026-10-15T00:00:00Z&limit=1", "Authorization: Bearer viewer")
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.NotContains(t, out.Body.String(), `"data":{"access_token"`)
	require.NotContains(t, out.Body.String(), "private-token")
	require.Contains(t, out.Body.String(), `"total":1`)
	out = f.api.Get("/platform/domain-events?tenant_id=2", "Authorization: Bearer viewer")
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), `"data":[]`)
	out = f.api.Get("/platform/domain-events?stream_seq=eq:1", "Authorization: Bearer viewer")
	require.Equal(t, http.StatusBadRequest, out.Code)
	out = f.api.Get(path, "Authorization: Bearer viewer")
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), "test detail")
	require.NotContains(t, out.Body.String(), "private-token")
	require.Equal(t, 1, f.audits)
	f.auditError = errors.New("audit unavailable")
	out = f.api.Get(path, "Authorization: Bearer viewer")
	require.Equal(t, http.StatusServiceUnavailable, out.Code)
	f.authority.current = false
	out = f.api.Get(path, "Authorization: Bearer manager")
	require.Equal(t, http.StatusForbidden, out.Code)
}
func TestEventsAdminParkedCASAndAuditRollback(t *testing.T) {
	f := newAdminFixture(t)
	r := f.park(t, "FAILED", 1)
	path := "/platform/parked-events/" + r.ID.String()
	body := `{"reason":"reviewed"}`
	out := f.api.Post(path+"/attempts", "Authorization: Bearer viewer", "If-Match: 1", strings.NewReader(body))
	require.Equal(t, http.StatusForbidden, out.Code)
	out = f.api.Post(path+"/attempts", "Authorization: Bearer manager", strings.NewReader(body))
	require.Equal(t, 428, out.Code)
	require.Contains(t, out.Body.String(), "171428")
	out = f.api.Post(path+"/attempts", "Authorization: Bearer manager", "If-Match: 2", strings.NewReader(body))
	require.Equal(t, http.StatusConflict, out.Code)
	require.Contains(t, out.Body.String(), "171409")
	f.auditError = errors.New("audit failed")
	out = f.api.Patch(path, "Authorization: Bearer manager", "If-Match: 1", strings.NewReader(`{"state":"DISCARDED","reason":"reviewed"}`))
	require.Equal(t, http.StatusServiceUnavailable, out.Code)
	q := query.BindParked(f.db, f.m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "PARKED", row.State)
	require.Equal(t, 1, row.Version)
	f.auditError = nil
	out = f.api.Post(path+"/attempts", "Authorization: Bearer manager", "If-Match: 1", strings.NewReader(body))
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Equal(t, `"2"`, out.Header().Get("ETag"))
	out = f.api.Patch(path, "Authorization: Bearer manager", "If-Match: 2", strings.NewReader(`{"state":"DISCARDED","reason":"operator discarded"}`))
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	out = f.api.Post(path+"/attempts", "Authorization: Bearer manager", "If-Match: 3", strings.NewReader(body))
	require.Equal(t, http.StatusConflict, out.Code)
	require.Contains(t, out.Body.String(), "171420")
}
func TestEventsAdminSubscriberPauseAndParkedFilters(t *testing.T) {
	f := newAdminFixture(t)
	f.park(t, "FAILED", 1)
	f.park(t, "BLOCKED", 2)
	out := f.api.Get("/platform/domain-event-subscribers", "Authorization: Bearer viewer")
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), `"name":"delivery.admin"`)
	require.Contains(t, out.Body.String(), `"blocked":1`)
	require.Contains(t, out.Body.String(), `"parked":1`)
	path := "/platform/domain-event-subscribers/delivery.admin"
	out = f.api.Get(path+"/parked-events?state=PARKED&reason=FAILED&subject_kind=eq:project&limit=1", "Authorization: Bearer viewer")
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), `"total":1`)
	out = f.api.Patch(path, "Authorization: Bearer manager", strings.NewReader(`{"paused":true}`))
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), `"paused":true`)
	f.auditError = errors.New("audit failed")
	out = f.api.Patch(path, "Authorization: Bearer manager", strings.NewReader(`{"paused":false}`))
	require.Equal(t, http.StatusServiceUnavailable, out.Code)
	out = f.api.Get("/platform/domain-event-subscribers", "Authorization: Bearer viewer")
	require.Equal(t, http.StatusOK, out.Code)
	require.Contains(t, out.Body.String(), `"paused":true`)
	out = f.api.Get("/platform/domain-event-subscribers/missing.subscriber/parked-events", "Authorization: Bearer viewer")
	require.Equal(t, http.StatusNotFound, out.Code)
}
func TestEventsAdminReplayStepupIdempotencyAndRollback(t *testing.T) {
	f := newAdminFixture(t)
	path := "/platform/domain-event-subscribers/delivery.admin/replays"
	body := `{"from_seq":1,"ignore_receipts":true,"reauth_proof":"fresh"}`
	out := f.api.Post(path, "Authorization: Bearer manager", "Idempotency-Key: replay-one", strings.NewReader(body))
	require.Equal(t, http.StatusForbidden, out.Code)
	f.auditError = errors.New("audit failed")
	out = f.api.Post(path, "Authorization: Bearer manager", "Cookie: browser=valid", "Idempotency-Key: replay-one", strings.NewReader(body))
	require.Equal(t, http.StatusServiceUnavailable, out.Code)
	var consumed int
	require.NoError(t, f.db.Raw("SELECT consumed FROM admin_proof_probe WHERE id=1").Scan(&consumed).Error)
	require.Zero(t, consumed)
	q := query.BindCursors(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Where(q.ReplayOf.Eq("delivery.admin")).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	f.auditError = nil
	out = f.api.Post(path, "Authorization: Bearer manager", "Cookie: browser=valid", "Idempotency-Key: replay-one", strings.NewReader(body))
	require.Equal(t, http.StatusCreated, out.Code, out.Body.String())
	firstBody := out.Body.String()
	calls, audits := f.proofCalls, f.audits
	var first struct {
		Data adminReplayView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &first))
	require.NotEmpty(t, first.Data.ReplayCursor)
	refreshedProofBody := strings.Replace(body, `"fresh"`, `"refreshed-proof"`, 1)
	out = f.api.Post(path, "Authorization: Bearer manager", "Cookie: browser=valid", "Idempotency-Key: replay-one", strings.NewReader(refreshedProofBody))
	require.Equal(t, http.StatusCreated, out.Code, out.Body.String())
	require.JSONEq(t, firstBody, out.Body.String(), "replay preserves the exact first response envelope")
	require.Equal(t, calls, f.proofCalls)
	require.Equal(t, audits, f.audits)
	require.Contains(t, out.Body.String(), first.Data.ReplayCursor)
	changed := strings.Replace(body, `"ignore_receipts":true`, `"ignore_receipts":false`, 1)
	out = f.api.Post(path, "Authorization: Bearer manager", "Cookie: browser=valid", "Idempotency-Key: replay-one", strings.NewReader(changed))
	require.Equal(t, http.StatusConflict, out.Code)
	f.authority.current = false
	out = f.api.Post(path, "Authorization: Bearer manager", "Cookie: browser=valid", "Idempotency-Key: replay-one", strings.NewReader(body))
	require.Equal(t, http.StatusForbidden, out.Code)
	f.authority.current = true
	require.NoError(t, f.db.Exec("UPDATE admin_proof_probe SET consumed=0").Error)
	out = f.api.Post(path, "Authorization: Bearer manager", "Cookie: browser=valid", "Idempotency-Key: replay-two", strings.NewReader(body))
	require.Equal(t, http.StatusConflict, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), "171422")
	out = f.api.Post(path, "Authorization: Bearer manager", "Cookie: browser=valid", "Idempotency-Key: replay-three", strings.NewReader(fmt.Sprintf(`{"from_seq":%d,"ignore_receipts":false,"reauth_proof":"fresh"}`, 100)))
	require.Equal(t, http.StatusConflict, out.Code, "active replay wins before range validation")
}

func TestEventsAdminFilterBoundsMissingResourcesAndStorageFailure(t *testing.T) {
	f := newAdminFixture(t)
	for _, query := range []string{"tenant_id=-1", "subject_id=0", "stream_seq=1", "stream_seq=gt:bad", "occurred_at=bad", "occurred_at=gte:bad", "occurred_at=eq:2026-10-15T00:00:00Z"} {
		out := f.api.Get("/platform/domain-events?"+query, "Authorization: Bearer viewer")
		require.Equal(t, http.StatusBadRequest, out.Code, out.Body.String())
	}
	for _, query := range []string{"stream_seq=lt:2", "occurred_at=gt:2026-10-14T00:00:00Z", "occurred_at=lt:2026-10-16T00:00:00Z", "occurred_at=lte:2026-10-16T00:00:00Z"} {
		out := f.api.Get("/platform/domain-events?"+query, "Authorization: Bearer viewer")
		require.Equal(t, http.StatusOK, out.Code, out.Body.String())
		require.Contains(t, out.Body.String(), `"total":1`)
	}
	out := f.api.Get("/platform/domain-events/999999", "Authorization: Bearer viewer")
	require.Equal(t, http.StatusNotFound, out.Code)
	out = f.api.Patch("/platform/domain-event-subscribers/missing.subscriber", "Authorization: Bearer manager", strings.NewReader(`{"paused":true}`))
	require.Equal(t, http.StatusNotFound, out.Code)
	out = f.api.Patch("/platform/parked-events/999999", "Authorization: Bearer manager", "If-Match: 1", strings.NewReader(`{"state":"DISCARDED","reason":"reviewed"}`))
	require.Equal(t, http.StatusNotFound, out.Code)
	out = f.api.Post("/platform/domain-event-subscribers/delivery.admin/replays", "Authorization: Bearer manager", "Cookie: browser=valid", "Idempotency-Key: unavailable-range", strings.NewReader(`{"from_seq":2,"to_seq":1,"ignore_receipts":false,"reauth_proof":"fresh"}`))
	require.Equal(t, http.StatusBadRequest, out.Code)
	require.Contains(t, out.Body.String(), "171401")
	out = f.api.Post("/platform/domain-event-subscribers/delivery.admin/replays", "Authorization: Bearer manager", "Cookie: browser=valid", "Idempotency-Key: expired-range", strings.NewReader(`{"from_seq":2,"ignore_receipts":false,"reauth_proof":"fresh"}`))
	require.Equal(t, http.StatusBadRequest, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), "171401")
	require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("admin_storage_unavailable", func(tx *gorm.DB) { _ = tx.AddError(errors.New("storage offline")) }))
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove("admin_storage_unavailable") })
	for _, path := range []string{"/platform/domain-events", "/platform/domain-events/" + f.eventID.String(), "/platform/domain-event-subscribers", "/platform/domain-event-subscribers/delivery.admin/parked-events"} {
		out = f.api.Get(path, "Authorization: Bearer viewer")
		require.Equal(t, http.StatusServiceUnavailable, out.Code, out.Body.String())
		require.NotContains(t, out.Body.String(), "storage offline")
	}
}

func TestEventsAdminDomainValidationRefusesInvalidRequestsAndCorruptData(t *testing.T) {
	for _, v := range []struct{ offset, limit int }{{-1, 1}, {0, 201}} {
		_, err := adminLimit(v.offset, v.limit)
		require.ErrorIs(t, err, errAdminInvalid)
	}
	limit, err := adminLimit(0, 0)
	require.NoError(t, err)
	require.Equal(t, 50, limit)
	_, err = adminEvent(&model.EventRow{Topics: "invalid"}, false)
	require.Error(t, err)
	_, err = adminEvent(&model.EventRow{Topics: "[]", Data: "invalid"}, true)
	require.Error(t, err)
	f := newAdminFixture(t)
	_, err = f.m.listAdminEvents(t.Context(), &adminEventListInput{Offset: -1})
	require.Error(t, err)
	_, err = f.m.listAdminParked(t.Context(), &adminParkedListInput{Limit: 201})
	require.Error(t, err)
	_, err = f.m.getAdminEvent(t.Context(), &adminEventInput{EventID: "bad"})
	require.Error(t, err)
	f.m.admin.idempotency = nil
	in := &adminReplayInput{}
	in.Body.FromSeq = 1
	_, err = f.m.replayAdminEvents(t.Context(), in)
	require.Error(t, err)
}
