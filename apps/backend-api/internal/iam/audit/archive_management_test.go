package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	auditi18n "7link.com/platform/internal/iam/audit/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArchiveManagementStatusTracksActualCompletion(t *testing.T) {
	h, _ := archiveJobFixture(t)
	status, err := h.service.archiveStatus(t.Context(), 11)
	require.NoError(t, err)
	assert.True(t, status.Enabled)
	assert.EqualValues(t, 2, status.TotalEvents)
	assert.EqualValues(t, 2, status.EligibleEvents)
	assert.EqualValues(t, 2, status.PendingEvents)
	assert.Zero(t, status.ArchivedEvents)
	job, err := h.service.requestArchive(t.Context(), 11)
	require.NoError(t, err)
	status, err = h.service.archiveStatus(t.Context(), 11)
	require.NoError(t, err)
	assert.EqualValues(t, 3, status.TotalEvents)
	assert.EqualValues(t, 1, status.QueuedJobs)
	assert.Zero(t, status.CompletedJobs)
	assert.EqualValues(t, 2, status.PendingEvents)
	_, err = h.service.runArchive(t.Context(), 11, job.ID, time.Minute)
	require.NoError(t, err)
	status, err = h.service.archiveStatus(t.Context(), 11)
	require.NoError(t, err)
	assert.EqualValues(t, 4, status.TotalEvents)
	assert.EqualValues(t, 2, status.EligibleEvents, "age eligibility is not a pending count")
	assert.EqualValues(t, 2, status.ArchivedEvents)
	assert.Zero(t, status.PendingEvents)
	assert.Zero(t, status.QueuedJobs)
	assert.EqualValues(t, 1, status.CompletedJobs)
	foreign, err := h.service.archiveStatus(t.Context(), 12)
	require.NoError(t, err)
	assert.EqualValues(t, 1, foreign.TotalEvents)
	assert.EqualValues(t, 1, foreign.PendingEvents)
	assert.Zero(t, foreign.CompletedJobs)
	h.service.archive = nil
	status, err = h.service.archiveStatus(t.Context(), 11)
	require.NoError(t, err)
	assert.False(t, status.Enabled)
	assert.EqualValues(t, 2, status.ArchivedEvents)
}

func TestArchiveManagementRetryUsesDatabaseLeaseAndPreservesSnapshot(t *testing.T) {
	h, _ := archiveJobFixture(t)
	job, err := h.service.requestArchive(t.Context(), 11)
	require.NoError(t, err)
	original, err := h.service.repo.archiveJob(t.Context(), 11, job.ID)
	require.NoError(t, err)
	queued, err := h.service.retryArchive(t.Context(), 11, job.ID)
	require.NoError(t, err)
	assert.Equal(t, job, queued)
	assert.EqualValues(t, 3, h.count(t, "iam_audit_events", 11), "queued retry is idempotent")
	_, err = h.service.retryArchive(t.Context(), 12, job.ID)
	assert.ErrorIs(t, err, ErrArchiveNotFound)
	table := h.table("iam_audit_archive_jobs")
	for _, state := range []string{"failed", "running"} {
		require.NoError(t, h.db.Table(table).Where("id = ?", job.ID).Updates(map[string]any{
			"state": state, "attempts": 2, "error_code": "interrupted", "next_attempt": time.Now().Add(time.Hour).UnixMilli(),
			"lease_token": 777, "lease_until": time.Now().Add(time.Hour).UnixMilli(),
		}).Error)
		status, statusErr := h.service.archiveStatus(t.Context(), 11)
		require.NoError(t, statusErr)
		if state == "running" {
			assert.EqualValues(t, 1, status.RunningJobs)
			_, err = h.service.retryArchive(t.Context(), 11, job.ID)
			assert.ErrorIs(t, err, ErrArchiveBusy, "service clock is years ahead but lease uses database time")
			require.NoError(t, h.db.Table(table).Where("id = ?", job.ID).Update("lease_until", 0).Error)
		} else {
			assert.EqualValues(t, 1, status.FailedJobs)
		}
		queued, err = h.service.retryArchive(t.Context(), 11, job.ID)
		require.NoError(t, err)
		assert.Equal(t, "queued", queued.State)
		assert.EqualValues(t, 2, queued.Attempts)
		assert.Zero(t, queued.NextAttempt)
		assert.Empty(t, queued.ErrorCode)
		row, readErr := h.service.repo.archiveJob(t.Context(), 11, job.ID)
		require.NoError(t, readErr)
		assert.Equal(t, original.Snapshot, row.Snapshot)
		assert.Zero(t, row.LeaseToken)
		assert.Zero(t, row.LeaseUntil)
	}
	_, err = h.service.runArchive(t.Context(), 11, job.ID, time.Minute)
	require.NoError(t, err)
	_, err = h.service.retryArchive(t.Context(), 11, job.ID)
	assert.ErrorIs(t, err, ErrArchiveBusy)
	result, err := h.service.Verify(t.Context(), 11)
	require.NoError(t, err)
	assert.True(t, result.Valid)
}

func TestArchiveManagementRetryAuditFailureRollsBack(t *testing.T) {
	h, _ := archiveJobFixture(t)
	job, err := h.service.requestArchive(t.Context(), 11)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.table("iam_audit_archive_jobs")).Where("id = ?", job.ID).Update("state", "failed").Error)
	h.service.repo.ids = auditFailingIDs{}
	_, err = h.service.retryArchive(t.Context(), 11, job.ID)
	require.Error(t, err)
	row, err := h.service.repo.archiveJob(t.Context(), 11, job.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", row.State)
}

func TestArchiveManagementWorkerIncludesPlatformScope(t *testing.T) {
	h, _ := archiveJobFixture(t)
	h.service.now = func() time.Time { return auditTestNow }
	_, err := h.service.Append(t.Context(), EventInput{TenantID: 0, EventType: "platform_event", Outcome: "success"})
	require.NoError(t, err)
	policy, err := h.service.setRetentionPolicy(t.Context(), 0, 30, 60)
	require.NoError(t, err)
	assert.Zero(t, policy.TenantID)
	h.service.now = func() time.Time { return auditTestNow.AddDate(0, 0, 731) }
	require.NoError(t, h.service.sweepArchives(t.Context(), time.Minute))
	jobs, err := h.service.listArchiveJobs(t.Context(), 0, 0, 10)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "complete", jobs[0].State)
	require.NotNil(t, jobs[0].Object)
	assert.Contains(t, jobs[0].Object.Key, "/0/")
	status, err := h.service.archiveStatus(t.Context(), 0)
	require.NoError(t, err)
	assert.EqualValues(t, 2, status.ArchivedEvents)
	assert.Zero(t, status.PendingEvents)
	assert.Equal(t, 60, status.Policy.ArchiveAfterDays)
	object, err := h.service.requestArchiveDownload(t.Context(), 0, jobs[0].ID)
	require.NoError(t, err)
	var payload bytes.Buffer
	require.NoError(t, h.service.writeArchiveDownload(t.Context(), object, &payload))
	assert.Contains(t, payload.String(), "platform_event")
	assert.NotContains(t, payload.String(), "archive_fixture")
	_, err = h.service.verifyArchiveJob(t.Context(), 11, jobs[0].ID)
	assert.ErrorIs(t, err, ErrArchiveNotFound)
}

type archivePermissionChecker struct {
	permission string
	platform   bool
}

type archiveTokenVerifier struct{ auditTokenVerifier }

func (v archiveTokenVerifier) Authenticate(ctx context.Context, authorization, cookie string) (*authnext.Claims, error) {
	if authorization != "Bearer test" {
		return nil, authnext.ErrInvalidToken
	}
	return v.auditTokenVerifier.Authenticate(ctx, authorization, cookie)
}

func (c *archivePermissionChecker) Check(_ context.Context, tenant guid.ID, permission string, _ guid.ID) (bool, error) {
	return tenant == 11 && c.permission == permission, nil
}

func (c *archivePermissionChecker) CheckPlatform(_ context.Context, permission string, _ guid.ID) (bool, error) {
	return c.platform && permission == "platform_administer", nil
}

func archiveManagementHTTP(t *testing.T, h *auditHarness, checker *archivePermissionChecker) humatest.TestAPI {
	t.Helper()
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	require.NoError(t, auditi18n.Register())
	_, api := humatest.New(t)
	RegisterHTTP(api, h.service, authz.NewRegistrar(authz.DefaultRegistry(), archiveTokenVerifier{}, checker, auditMembershipChecker{}))
	return api
}

func TestArchiveManagementHTTPPermissionsAndTenantHeaders(t *testing.T) {
	h, _ := archiveJobFixture(t)
	checker := &archivePermissionChecker{}
	api := archiveManagementHTTP(t, h, checker)
	for _, route := range []struct {
		path       string
		permission string
		call       func(string, ...any) *httptest.ResponseRecorder
	}{
		{"/archive-status", "audit_event_view", api.Get},
		{"/archives", "audit_event_view", api.Get},
		{"/archives", "audit_event_export", api.Post},
		{"/archives/999/attempts", "audit_event_export", api.Post},
		{"/archives/999/verifications", "audit_event_view", api.Post},
		{"/archives/999/content", "audit_event_export", api.Get},
	} {
		checker.permission, checker.platform = "", false
		path := "/audit" + route.path
		assert.Equal(t, http.StatusUnauthorized, route.call(path, "X-Tenant-Id: 11").Code, path)
		assert.Equal(t, http.StatusForbidden, route.call(path, "Authorization: Bearer test", "X-Tenant-Id: 11").Code, path)
		checker.permission = route.permission
		allowed := route.call(path, "Authorization: Bearer test", "X-Tenant-Id: 11")
		assert.Contains(t, []int{http.StatusOK, http.StatusNotFound}, allowed.Code, allowed.Body.String())
		assert.Equal(t, http.StatusForbidden, route.call(path, "Authorization: Bearer test", "X-Tenant-Id: 12").Code, path)
		for _, invalid := range []string{"0", "invalid", "-1"} {
			assert.Equal(t, http.StatusForbidden, route.call(path, "Authorization: Bearer test", "X-Tenant-Id: "+invalid).Code, path)
		}
		platformPath := "/platform" + path
		assert.Equal(t, http.StatusForbidden, route.call(platformPath, "Authorization: Bearer test").Code, platformPath)
		checker.platform = true
		platformResult := route.call(platformPath, "Authorization: Bearer test")
		assert.Contains(t, []int{http.StatusOK, http.StatusNotFound, http.StatusUnprocessableEntity}, platformResult.Code, platformResult.Body.String())
		assert.Equal(t, http.StatusUnprocessableEntity, route.call(platformPath, "Authorization: Bearer test", "X-Tenant-Id: 11").Code, platformPath)
	}
	checker.permission, checker.platform = "audit_event_view", false
	assert.Equal(t, http.StatusForbidden, api.Post("/audit/archives", "Authorization: Bearer test", "X-Tenant-Id: 11").Code)
	checker.permission = "audit_event_export"
	assert.Equal(t, http.StatusForbidden, api.Get("/audit/archive-status", "Authorization: Bearer test", "X-Tenant-Id: 11").Code)
}

func TestArchiveManagementHTTPDownloadsVerifiedVersionAndRejectsTampering(t *testing.T) {
	h, fixture := archiveJobFixture(t)
	checker := &archivePermissionChecker{permission: "audit_event_export"}
	api := archiveManagementHTTP(t, h, checker)
	queued := api.Post("/audit/archives", "Authorization: Bearer test", "X-Tenant-Id: 11")
	require.Equal(t, http.StatusOK, queued.Code, queued.Body.String())
	var response struct {
		Data ArchiveJob `json:"data"`
	}
	require.NoError(t, json.Unmarshal(queued.Body.Bytes(), &response))
	job, err := h.service.runArchive(t.Context(), 11, response.Data.ID, time.Minute)
	require.NoError(t, err)
	path := "/audit/archives/" + job.ID.String()
	checker.permission = "audit_event_view"
	verified := api.Post(path+"/verifications", "Authorization: Bearer test", "X-Tenant-Id: 11")
	require.Equal(t, http.StatusOK, verified.Code, verified.Body.String())
	listed := api.Get("/audit/archives?offset=1&limit=1", "Authorization: Bearer test", "X-Tenant-Id: 11")
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	assert.Contains(t, listed.Body.String(), `"data":[]`)
	assert.Contains(t, listed.Body.String(), `"total":1`)
	checker.permission = "audit_event_export"
	download := api.Get(path+"/content", "Authorization: Bearer test", "X-Tenant-Id: 11")
	require.Equal(t, http.StatusOK, download.Code, download.Body.String())
	assert.Equal(t, "application/x-ndjson", download.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", download.Header().Get("Cache-Control"))
	assert.Equal(t, strconv.FormatInt(job.Object.Size, 10), download.Header().Get("Content-Length"))
	assert.Contains(t, download.Header().Get("Content-Disposition"), job.ID.String()+".ndjson")
	fixture.mu.Lock()
	assert.Equal(t, fixture.objects[job.Object.Key], download.Body.Bytes())
	fixture.objects[job.Object.Key][1] ^= 1
	fixture.mu.Unlock()
	corrupted := api.Get(path+"/content", "Authorization: Bearer test", "X-Tenant-Id: 11")
	assert.Equal(t, http.StatusConflict, corrupted.Code, corrupted.Body.String())
	assert.NotContains(t, corrupted.Body.String(), "archive_fixture", "no archive bytes before verification")
}

func TestArchiveManagementValidationDisabledAndDatabaseErrors(t *testing.T) {
	h, _ := archiveJobFixture(t)
	_, err := h.service.archiveStatus(t.Context(), -1)
	assert.ErrorIs(t, err, ErrArchiveInvalid)
	_, err = h.service.retryArchive(t.Context(), -1, 1)
	assert.ErrorIs(t, err, ErrArchiveInvalid)
	_, err = h.service.verifyArchiveJob(t.Context(), 11, 0)
	assert.ErrorIs(t, err, ErrArchiveInvalid)
	_, err = (*Service)(nil).archiveStatus(t.Context(), 11)
	assert.Error(t, err)
	_, err = (*Service)(nil).verifyArchiveJob(t.Context(), 11, 1)
	assert.ErrorIs(t, err, ErrArchiveDisabled)
	_, err = (*Service)(nil).retryArchive(t.Context(), 11, 1)
	assert.ErrorIs(t, err, ErrArchiveDisabled)
	assert.ErrorIs(t, (*Service)(nil).writeArchiveDownload(t.Context(), ArchiveObject{}, io.Discard), ErrArchiveDisabled)
	assert.ErrorIs(t, h.service.writeArchiveDownload(t.Context(), ArchiveObject{}, nil), ErrArchiveInvalid)
	job, err := h.service.requestArchive(t.Context(), 11)
	require.NoError(t, err)
	_, err = h.service.verifyArchiveJob(t.Context(), 11, job.ID)
	assert.ErrorIs(t, err, ErrArchiveBusy)
	_, err = h.service.requestArchiveDownload(t.Context(), 12, job.ID)
	assert.ErrorIs(t, err, ErrArchiveNotFound)
	require.NoError(t, h.db.Exec("DROP TABLE "+h.table("iam_audit_retention_policies")).Error)
	_, err = h.service.archiveStatus(t.Context(), 11)
	assert.Error(t, err)
	require.NoError(t, h.db.Exec("DROP TABLE "+h.table("iam_audit_archive_jobs")).Error)
	_, err = h.service.repo.archiveJobCount(t.Context(), 11)
	assert.Error(t, err)
	_, err = h.service.listArchiveJobs(t.Context(), 11, 0, 10)
	assert.Error(t, err)
}

func TestArchiveManagementRejectsCorruptJobMetadataAndInterruptedDownloads(t *testing.T) {
	h, fixture := archiveJobFixture(t)
	job, err := h.service.requestArchive(t.Context(), 11)
	require.NoError(t, err)
	job, err = h.service.runArchive(t.Context(), 11, job.ID, time.Minute)
	require.NoError(t, err)
	for _, alter := range []func(*ArchiveObject){
		func(o *ArchiveObject) { o.Key = "audit/archives/12/foreign.ndjson" },
		func(o *ArchiveObject) { o.Size = 0 },
		func(o *ArchiveObject) { o.Bucket = "foreign" },
		func(o *ArchiveObject) { o.RetainUntil = job.RetainUntil.Add(-time.Second) },
		func(o *ArchiveObject) { o.VersionID = "" },
	} {
		object := *job.Object
		alter(&object)
		encoded, encodeErr := json.Marshal(object)
		require.NoError(t, encodeErr)
		require.NoError(t, h.db.Table(h.table("iam_audit_archive_jobs")).Where("id = ?", job.ID).Update("object", string(encoded)).Error)
		_, err = h.service.verifyArchiveJob(t.Context(), 11, job.ID)
		assert.ErrorIs(t, err, ErrArchiveIntegrity)
	}
	assert.ErrorIs(t, h.service.writeArchiveDownload(t.Context(), *job.Object, errorAuditWriter{}), ErrArchiveUnavailable)
	fixture.mu.Lock()
	fixture.objects[job.Object.Key][1] ^= 1
	fixture.mu.Unlock()
	assert.ErrorIs(t, h.service.writeArchiveDownload(t.Context(), *job.Object, io.Discard), ErrArchiveIntegrity)
	fixture.mu.Lock()
	fixture.failMethod, fixture.failContains = http.MethodGet, ".ndjson"
	fixture.mu.Unlock()
	assert.ErrorIs(t, h.service.writeArchiveDownload(t.Context(), *job.Object, io.Discard), ErrArchiveUnavailable)
}

func TestArchiveManagementHTTPHandlerValidationAndPlatformRetention(t *testing.T) {
	h, _ := archiveJobFixture(t)
	_, api := humatest.New(t)
	respx.Install()
	RegisterHTTP(api, h.service, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	for _, route := range []struct {
		path string
		call func(string, ...any) *httptest.ResponseRecorder
	}{
		{"/archive-status", api.Get}, {"/archives", api.Get}, {"/archives", api.Post},
		{"/archives/1/attempts", api.Post}, {"/archives/1/verifications", api.Post}, {"/archives/1/content", api.Get},
	} {
		for _, tenant := range []string{"", "0", "-1", "bad"} {
			response := route.call("/audit"+route.path, "X-Tenant-Id: "+tenant)
			assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
		}
	}
	for _, suffix := range []string{"attempts", "verifications"} {
		assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/audit/archives/bad/"+suffix, "X-Tenant-Id: 11").Code)
	}
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/audit/archives/bad/content", "X-Tenant-Id: 11").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/audit/archives?limit=201", "X-Tenant-Id: 11").Code)
	checker := &archivePermissionChecker{platform: true}
	secured := archiveManagementHTTP(t, h, checker)
	body := map[string]any{"min_days": 30, "archive_after_days": 60}
	response := secured.Put("/platform/audit/retention-policy", "Authorization: Bearer test", body)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = secured.Get("/platform/audit/archive-status", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"archive_after_days":60`)
	assert.Equal(t, http.StatusUnprocessableEntity, secured.Put("/platform/audit/retention-policy", "Authorization: Bearer test", "X-Tenant-Id: 11", body).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, secured.Put("/platform/audit/retention-policy", "Authorization: Bearer test", map[string]any{"min_days": 60, "archive_after_days": 30}).Code)
	checker.platform = false
	assert.Equal(t, http.StatusForbidden, secured.Put("/platform/audit/retention-policy", "Authorization: Bearer test", body).Code)
	h.service.archive = nil
	assert.Equal(t, http.StatusServiceUnavailable, api.Post("/audit/archives", "X-Tenant-Id: 11").Code)
	assert.Equal(t, http.StatusServiceUnavailable, api.Post("/audit/archives/1/attempts", "X-Tenant-Id: 11").Code)
	assert.Equal(t, http.StatusServiceUnavailable, api.Get("/audit/archives/1/content", "X-Tenant-Id: 11").Code)
}
