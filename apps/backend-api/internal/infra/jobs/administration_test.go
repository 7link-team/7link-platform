package jobs

import (
	"7link.com/platform/internal/infra/guid"
	jobsi18n "7link.com/platform/internal/infra/jobs/i18n"
	"7link.com/platform/internal/infra/jobs/model"
	"7link.com/platform/internal/infra/jobs/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestAdminActivityPresentationUsesRequestLocaleAndExplicitModule(t *testing.T) {
	require.NoError(t, i18n.NewModule(nil, "en-US").Initialize(t.Context()))
	require.NoError(t, i18n.RegisterFS("job-activity-test", fstest.MapFS{
		"locales/en-US.json": {Data: []byte(`{"name":"Reconcile roles","purpose":"Apply declared role seeds."}`)},
		"locales/zh-CN.json": {Data: []byte(`{"name":"同步角色","purpose":"应用已声明的角色种子。"}`)},
		"locales/ms-MY.json": {Data: []byte(`{"name":"Segerakkan peranan","purpose":"Gunakan benih peranan yang diisytiharkan."}`)},
	}, "locales"))
	m := catalogModule()
	handler := catalogHandler("governance.seed_reconcile")
	handler.Module = "business_governance"
	handler.DisplayNameKey = "job-activity-test.name"
	handler.DescriptionKey = "job-activity-test.purpose"
	require.NoError(t, m.AddHandlers("governance", handler))
	for locale, name := range map[string]string{"en-US": "Reconcile roles", "zh-CN": "同步角色", "ms-MY": "Segerakkan peranan"} {
		ctx := i18n.WithLocale(t.Context(), locale)
		view, err := m.adminJob(ctx, &model.Job{Kind: handler.Kind}, false)
		require.NoError(t, err)
		require.Equal(t, "business_governance", view.Module)
		require.Equal(t, name, view.DisplayName)
		require.NotEmpty(t, view.Description)
		schedule := m.adminSchedule(ctx, &model.Schedule{Kind: handler.Kind})
		require.Equal(t, view.Module, schedule.Module)
		require.Equal(t, view.DisplayName, schedule.DisplayName)
		require.Equal(t, view.Description, schedule.Description)
	}
	unknown, err := m.adminJob(t.Context(), &model.Job{Kind: "retired.handler"}, false)
	require.NoError(t, err)
	require.False(t, unknown.HasHandler)
	require.Empty(t, unknown.Module)
	require.Empty(t, unknown.DisplayName)
	require.Empty(t, unknown.Description)
}

type adminHTTPAuthority struct{ current bool }

func (a *adminHTTPAuthority) Authenticate(_ context.Context, authorization, _ string) (*authn.Claims, error) {
	actor := guid.ID(1)
	if authorization == "Bearer viewer" {
		actor = 2
	} else if authorization == "Bearer tenant" {
		actor = 3
	} else if authorization != "Bearer manager" {
		return nil, authn.ErrInvalidCredentials
	}
	return &authn.Claims{PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal}, nil
}
func (a *adminHTTPAuthority) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return false, nil
}
func (a *adminHTTPAuthority) CheckPlatform(_ context.Context, permission string, actor guid.ID) (bool, error) {
	return actor == 1 || (actor == 2 && permission == "system_job_view"), nil
}
func (a *adminHTTPAuthority) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

func adminAPI(t *testing.T, m *Module) (humatest.TestAPI, *adminHTTPAuthority) {
	require.NoError(t, i18n.NewModule(nil, "en-US").Initialize(t.Context()))
	require.NoError(t, jobsi18n.Register())
	t.Helper()
	a := &adminHTTPAuthority{current: true}
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	registrar := authz.NewRegistrar(registry, a, a, a)
	services := module.NewServices()
	require.NoError(t, m.provideAdmin(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[AdminConnector](services)
	require.NoError(t, err)
	require.NoError(t, connector.BindAudit(func(context.Context, *gorm.DB, string, guid.ID, string, guid.ID, map[string]any) error { return nil }))
	require.NoError(t, connector.Bind(registrar, func(ctx context.Context, tx *gorm.DB, code string, actor guid.ID) (bool, error) {
		require.NotNil(t, tx)
		if !a.current {
			return false, nil
		}
		return a.CheckPlatform(ctx, code, actor)
	}))
	_, api := humatest.New(t)
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	return api, a
}

func TestPlatformJobsHTTPAuthorityRedactionAndCAS(t *testing.T) {
	m, db := queueHarness(t, 100)
	job, err := m.Enqueue(t.Context(), nil, EnqueueRequest{TenantID: 7, Kind: "jobs.work", Payload: map[string]any{"client_secret": "private-credential", "order_id": "7"}})
	require.NoError(t, err)
	q := query.BindJobs(db, m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(job.JobID)).Update(q.State, "DEAD")
	require.NoError(t, err)
	api, a := adminAPI(t, m)
	path := "/platform/jobs/" + job.JobID.String()
	out := api.Get("/platform/jobs", "Authorization: Bearer tenant")
	require.Equal(t, http.StatusForbidden, out.Code)
	out = api.Get("/platform/jobs", "Authorization: Bearer viewer")
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.NotContains(t, out.Body.String(), "private-credential")
	require.NotContains(t, out.Body.String(), `"payload"`)
	out = api.Get(path, "Authorization: Bearer viewer")
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.NotContains(t, out.Body.String(), "private-credential")
	require.Contains(t, out.Body.String(), "REDACTED")
	require.Equal(t, `"1"`, out.Header().Get("ETag"))
	body := `{"reason":"operator checked dependency"}`
	out = api.Post(path+"/attempts", "Authorization: Bearer viewer", "If-Match: 1", strings.NewReader(body))
	require.Equal(t, http.StatusForbidden, out.Code)
	out = api.Post(path+"/attempts", "Authorization: Bearer manager", strings.NewReader(body))
	require.Equal(t, 428, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), `"code":170428`)
	a.current = false
	out = api.Post(path+"/attempts", "Authorization: Bearer manager", "If-Match: 1", strings.NewReader(body))
	require.Equal(t, http.StatusForbidden, out.Code)
	a.current = true
	out = api.Post(path+"/attempts", "Authorization: Bearer manager", "If-Match: 1", strings.NewReader(body))
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), `"manual_retries":1`)
	require.Contains(t, out.Body.String(), `"state":"PENDING"`)
	out = api.Post(path+"/attempts", "Authorization: Bearer manager", "If-Match: 1", strings.NewReader(body))
	require.Equal(t, http.StatusConflict, out.Code)
	out = api.Post(path+"/attempts", "Authorization: Bearer manager", "If-Match: 2", strings.NewReader(body))
	require.Equal(t, http.StatusConflict, out.Code)
	out = api.Patch(path, "Authorization: Bearer manager", "If-Match: 2", strings.NewReader(`{"state":"CANCELLED","reason":"discard"}`)) //nolint:misspell // Spec freezes this protocol state spelling.
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), `"state":"CANCELLED"`) //nolint:misspell // Spec freezes this protocol state spelling.
	out = api.Get("/platform/jobs?priority=lte:3&tenant_id=eq:7&created_at=gte:"+time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "Authorization: Bearer viewer")
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	out = api.Get("/platform/jobs?priority=lte:no", "Authorization: Bearer viewer")
	require.Equal(t, http.StatusBadRequest, out.Code, out.Body.String())
}

func TestPlatformJobsAuditFailureRollsBackRetry(t *testing.T) {
	m, db := queueHarness(t, 100)
	job, err := m.Enqueue(t.Context(), nil, EnqueueRequest{Kind: "jobs.work"})
	require.NoError(t, err)
	q := query.BindJobs(db, m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(job.JobID)).Update(q.State, "DEAD")
	require.NoError(t, err)
	api, _ := adminAPI(t, m)
	m.admin.audit = func(_ context.Context, tx *gorm.DB, action string, tenant guid.ID, resource string, id guid.ID, detail map[string]any) error {
		require.NotNil(t, tx)
		require.Equal(t, "jobs.retried", action)
		require.Equal(t, job.JobID, id)
		require.Equal(t, "reviewed", detail["reason"])
		return errors.New("audit unavailable")
	}
	out := api.Post("/platform/jobs/"+job.JobID.String()+"/attempts", "Authorization: Bearer manager", "If-Match: 1", strings.NewReader(`{"reason":"reviewed"}`))
	require.Equal(t, http.StatusServiceUnavailable, out.Code, out.Body.String())
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(job.JobID)).First()
	require.NoError(t, err)
	require.Equal(t, "DEAD", row.State)
	require.Equal(t, 1, row.Version)
	require.Zero(t, row.ManualRetries)
}

func TestPlatformJobsSummarySpansPagesAndStatesWithinTenant(t *testing.T) {
	m, db := queueHarness(t, 100)
	q := query.BindJobs(db, m.names)
	clockMS, err := gormx.DatabaseClockMillis(t.Context(), db)
	require.NoError(t, err)
	now := time.UnixMilli(clockMS).UTC()
	for _, item := range []struct {
		tenant guid.ID
		state  string
		age    time.Duration
	}{
		{7, "PENDING", time.Hour}, {7, "PENDING", 2 * time.Hour}, {7, "RUNNING", time.Hour},
		{7, "DEAD", time.Hour}, {7, "DEAD", 25 * time.Hour}, {8, "PENDING", 4 * time.Hour}, {8, "RUNNING", time.Hour},
	} {
		job, err := m.Enqueue(t.Context(), nil, EnqueueRequest{TenantID: item.tenant, Kind: "jobs.work"})
		require.NoError(t, err)
		at := now.Add(-item.age)
		_, err = q.WithContext(t.Context()).Where(q.ID.Eq(job.JobID)).UpdateSimple(q.State.Value(item.state), q.CreatedAt.Value(at), q.FinishedAt.Value(at))
		require.NoError(t, err)
	}
	api, _ := adminAPI(t, m)
	for _, item := range []struct {
		query            string
		pending, running int64
	}{
		{"?state=DEAD&limit=1&tenant_id=7", 2, 1}, {"?state=DEAD&limit=1", 3, 2},
	} {
		out := api.Get("/platform/jobs"+item.query, "Authorization: Bearer viewer")
		require.Equal(t, http.StatusOK, out.Code, out.Body.String())
		var body struct {
			Data adminJobPage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
		require.Len(t, body.Data.Items, 1)
		require.Equal(t, item.pending, body.Data.Summary.Pending, out.Body.String())
		require.Equal(t, item.running, body.Data.Summary.Running)
		require.EqualValues(t, 1, body.Data.Summary.Dead24h)
		require.GreaterOrEqual(t, body.Data.Summary.OldestPendingAgeSeconds, int64((2 * time.Hour).Seconds()))
	}
}

func TestPlatformJobsRetryEnforcesPlatformPendingQuota(t *testing.T) {
	m, db := queueHarness(t, 1)
	dead, err := m.Enqueue(t.Context(), nil, EnqueueRequest{Kind: "jobs.work"})
	require.NoError(t, err)
	q := query.BindJobs(db, m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(dead.JobID)).Update(q.State, "DEAD")
	require.NoError(t, err)
	_, err = m.Enqueue(t.Context(), nil, EnqueueRequest{Kind: "jobs.work"})
	require.NoError(t, err)
	api, _ := adminAPI(t, m)
	out := api.Post("/platform/jobs/"+dead.JobID.String()+"/attempts", "Authorization: Bearer manager", "If-Match: 1", strings.NewReader(`{"reason":"reviewed"}`))
	require.Equal(t, http.StatusServiceUnavailable, out.Code, out.Body.String())
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(dead.JobID)).First()
	require.NoError(t, err)
	require.Equal(t, "DEAD", row.State)
	require.Equal(t, 1, row.Version)
}

func TestPlatformJobsBatchAndScheduleOperations(t *testing.T) {
	m, db := queueHarness(t, 100)
	m.mu.Lock()
	m.sealed = false
	m.mu.Unlock()
	require.NoError(t, m.AddSchedules("jobs", Schedule{Name: "jobs.admin_hourly", Kind: "jobs.work", Interval: time.Hour, TenantScope: ScopePlatform}))
	require.NoError(t, m.Initialize(t.Context()))
	require.NoError(t, m.reconcileSchedules(t.Context()))
	q := query.BindSchedules(db, m.names)
	schedule, err := q.WithContext(t.Context()).First()
	require.NoError(t, err)
	api, _ := adminAPI(t, m)
	path := "/platform/job-schedules/" + schedule.ID.String()
	for _, key := range []string{"", "bad key", "不可重放", strings.Repeat("x", 65)} {
		_, err := m.runAdminSchedule(t.Context(), &adminScheduleRunInput{AdminScheduleInput: AdminScheduleInput{ScheduleID: schedule.ID.String()}, Key: key})
		var status huma.StatusError
		require.ErrorAs(t, err, &status)
		require.Equal(t, http.StatusBadRequest, status.GetStatus())
	}
	out := api.Get("/platform/job-schedules", "Authorization: Bearer viewer")
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), `"orphaned":false`)
	out = api.Patch(path, "Authorization: Bearer manager", "If-Match: 1", strings.NewReader(`{"enabled":false}`))
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), `"enabled":false`)
	out = api.Patch(path, "Authorization: Bearer manager", "If-Match: 1", strings.NewReader(`{"enabled":true}`))
	require.Equal(t, http.StatusConflict, out.Code)
	first := api.Post(path+"/runs", "Authorization: Bearer manager", "Idempotency-Key: manual-once")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	replay := api.Post(path+"/runs", "Authorization: Bearer manager", "Idempotency-Key: manual-once")
	require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	var a, b map[string]any
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &a))
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &b))
	require.Equal(t, a["data"], b["data"])
	out = api.Post(path+"/runs", "Authorization: Bearer manager", "Idempotency-Key: manual-other")
	require.Equal(t, http.StatusConflict, out.Code)
	job, err := m.Enqueue(t.Context(), nil, EnqueueRequest{Kind: "jobs.work"})
	require.NoError(t, err)
	jq := query.BindJobs(db, m.names)
	_, err = jq.WithContext(t.Context()).Where(jq.ID.Eq(job.JobID)).Update(jq.State, "DEAD")
	require.NoError(t, err)
	out = api.Post("/platform/job-attempts", "Authorization: Bearer manager", strings.NewReader(fmt.Sprintf(`{"job_ids":[%q,"0"],"reason":"checked"}`, job.JobID.String())))
	require.Equal(t, http.StatusUnprocessableEntity, out.Code, out.Body.String())
	unchanged, err := jq.WithContext(t.Context()).Where(jq.ID.Eq(job.JobID)).First()
	require.NoError(t, err)
	require.Equal(t, "DEAD", unchanged.State, "invalid batch must not partially retry valid items")
	out = api.Post("/platform/job-attempts", "Authorization: Bearer manager", strings.NewReader(fmt.Sprintf(`{"job_ids":[%q,"999"],"reason":"checked"}`, job.JobID.String())))
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), job.JobID.String())
	require.Contains(t, out.Body.String(), `"code":170404`)
}

func TestNotificationQueueAdmissionUsesOwnerQuotaGuard(t *testing.T) {
	m, db := queueHarness(t, 3)
	seen := ""
	m.guard = func(_ context.Context, _ *gorm.DB, key string) error { seen = key; return nil }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.admitQueueOn(t.Context(), tx, 7) }))
	require.Equal(t, "jobs:pending:7", seen)
	require.ErrorIs(t, m.admitQueueOn(t.Context(), nil, 7), ErrInvalidRequest)
	require.ErrorIs(t, m.admitQueueOn(t.Context(), db, -1), ErrInvalidRequest)
	require.ErrorIs(t, (*Module)(nil).admitQueueOn(t.Context(), db, 7), ErrInvalidRequest)
	denied := errors.New("quota lock unavailable")
	m.guard = func(context.Context, *gorm.DB, string) error { return denied }
	require.ErrorIs(t, m.admitQueueOn(t.Context(), db, 7), denied)
}

func TestAdminJobDetailKeepsRecordedTraceAndStart(t *testing.T) {
	m := &Module{handlers: map[string]Handler{}}
	started := time.Date(2026, 10, 20, 1, 2, 3, 0, time.UTC)
	row := &model.Job{ID: 91, Kind: "example", Payload: `{}`, ErrorHistory: `[]`, CorrelationID: "recorded-correlation", StartedAt: &started}
	detail, err := m.adminJob(t.Context(), row, true)
	require.NoError(t, err)
	require.Equal(t, "recorded-correlation", detail.CorrelationID)
	require.Equal(t, &started, detail.StartedAt)
	list, err := m.adminJob(t.Context(), row, false)
	require.NoError(t, err)
	require.Empty(t, list.CorrelationID)
	require.Nil(t, list.StartedAt)
}
