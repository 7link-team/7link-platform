package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArchiveWorkerStartsProcessesAndStops(t *testing.T) {
	h, fixture := archiveJobFixture(t)
	h.service.now = func() time.Time { return time.Now().UTC().AddDate(0, 0, -2) }
	_, err := h.service.Append(t.Context(), EventInput{TenantID: 21, EventType: "old", Outcome: "success"})
	require.NoError(t, err)
	h.service.now = time.Now
	_, err = h.service.SetRetentionPolicy(t.Context(), 21, 1, 1)
	require.NoError(t, err)
	_, server := fakeAnchorStore(t, fixture, "unused")
	instance := NewModule(h.runtime, Config{
		Anchor:  AnchorConfig{Endpoint: server.URL, Bucket: "audit-worker", AccessKey: "fixture", SecretKey: "fixture", RetentionDays: 1},
		Archive: ArchiveConfig{Enabled: true, Interval: time.Second, Timeout: time.Second},
	})
	instance.service, instance.ids = h.service, h.service.repo.ids
	require.NoError(t, instance.Start(t.Context()))
	t.Cleanup(func() { require.NoError(t, instance.Stop(context.Background())) })
	require.NotNil(t, instance.service.archive)
	assert.Nil(t, instance.service.anchor, "archive execution does not implicitly enable anchoring")
	firstService := instance.service
	require.NoError(t, instance.Start(t.Context()))
	assert.Same(t, firstService, instance.service)
	require.Eventually(t, func() bool {
		jobs, err := instance.service.ListArchiveJobs(t.Context(), 21, 0, 10)
		return err == nil && len(jobs) == 1 && jobs[0].State == "complete"
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, instance.Stop(t.Context()))
	require.NoError(t, instance.Stop(t.Context()))
	instance.workerMu.Lock()
	assert.Nil(t, instance.workerDone)
	instance.workerMu.Unlock()
}

func TestArchiveWorkerValidationAndUnavailableDatabase(t *testing.T) {
	for _, cfg := range []ArchiveConfig{
		{}, {Interval: time.Second, Timeout: 0}, {Interval: time.Second, Timeout: 2 * time.Hour},
	} {
		assert.Error(t, validateArchiveWorker(cfg))
	}
	assert.NoError(t, validateArchiveWorker(ArchiveConfig{Interval: time.Second, Timeout: time.Second}))
	assert.NoError(t, (*Module)(nil).Stop(t.Context()))
	assert.NoError(t, (&Module{}).Stop(t.Context()))
	assert.ErrorIs(t, (*Service)(nil).sweepArchives(t.Context(), time.Second), errRepositoryUnavailable)
	h, _ := archiveJobFixture(t)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, h.service.sweepArchives(canceled, time.Second), context.Canceled)
	assert.Equal(t, "integrity_failed", archiveFailureCode(ErrArchiveIntegrity))
	assert.Equal(t, "interrupted", archiveFailureCode(context.DeadlineExceeded))
	assert.Equal(t, "storage_unavailable", archiveFailureCode(errors.New("private infrastructure details")))
	done := make(chan struct{})
	instance := &Module{workerDone: done, workerCancel: func() {}}
	assert.ErrorIs(t, instance.Stop(canceled), context.Canceled)
	close(done)
	assert.NoError(t, instance.Stop(t.Context()))
}

func TestArchiveWorkerRejectsUnmigratedSchemaAndInvalidConfiguration(t *testing.T) {
	h := newAuditHarness(t, "")
	fixture := newFakeS3()
	_, server := fakeAnchorStore(t, fixture, "unused")
	config := Config{
		Anchor:  AnchorConfig{Endpoint: server.URL, Bucket: "audit-worker-schema", AccessKey: "fixture", SecretKey: "fixture", RetentionDays: 1},
		Archive: ArchiveConfig{Enabled: true, Interval: time.Second, Timeout: time.Second},
	}
	instance := NewModule(h.runtime, config)
	instance.service, instance.ids = h.service, h.service.repo.ids
	assert.ErrorContains(t, instance.Start(t.Context()), "apply audit v3 migration")
	assert.Nil(t, instance.workerDone)
	instance.config.Archive.Timeout = 0
	assert.ErrorContains(t, instance.Start(t.Context()), "archive configuration")
	instance.config.Archive.Timeout = time.Second
	instance.config.Anchor.RetentionDays = maxRetentionDays + 1
	assert.ErrorIs(t, instance.Start(t.Context()), ErrAnchorMisconfigured)
}

func TestArchiveJobRealStoreCompletesFromDurableRequest(t *testing.T) {
	endpoint := os.Getenv("IAM_ARCHIVE_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("set IAM_ARCHIVE_MINIO_ENDPOINT to an isolated MinIO fixture")
	}
	h, _ := archiveJobFixture(t)
	store, err := NewAnchorStore(AnchorConfig{
		Endpoint: endpoint, Bucket: "audit-job-real-" + time.Now().UTC().Format("20060102-150405.000000000"),
		AccessKey: auditMinIOTestUser, SecretKey: auditMinIOTestPassword, RetentionDays: 1,
	})
	require.NoError(t, err)
	h.service.archive = store
	job, err := h.service.RequestArchive(t.Context(), 11)
	require.NoError(t, err)
	restarted := NewService(h.runtime, h.service.repo.ids)
	restarted.archive, restarted.now = store, h.service.now
	completed, err := restarted.RunArchive(t.Context(), 11, job.ID, time.Minute)
	require.NoError(t, err)
	require.Equal(t, "complete", completed.State)
	require.NotNil(t, completed.Object)
	_, err = store.verifyArchive(t.Context(), *completed.Object)
	require.NoError(t, err)
	row, err := restarted.repo.archiveJob(t.Context(), 11, job.ID)
	require.NoError(t, err)
	assert.Contains(t, row.Object, completed.Object.VersionID)
	assert.Contains(t, row.Object, completed.Object.SHA256)
	assert.Zero(t, row.LeaseToken)
	assert.Zero(t, row.LeaseUntil)
	api := archiveManagementHTTP(t, &auditHarness{service: restarted}, &archivePermissionChecker{permission: "audit_event_export"})
	download := api.Get("/iam/audit/archives/"+completed.ID.String()+"/download", "Authorization: Bearer test", "X-Tenant-Id: 11")
	require.Equal(t, http.StatusOK, download.Code, download.Body.String())
	sum := sha256.Sum256(download.Body.Bytes())
	assert.Equal(t, completed.Object.SHA256, hex.EncodeToString(sum[:]))
	assert.Equal(t, completed.Object.Size, int64(download.Body.Len()))
	assert.Contains(t, download.Body.String(), `"type":"complete"`)
	// A newer mutable alias must not change what the management download serves.
	_, err = store.client.PutObject(t.Context(), store.bucket, completed.Object.Key, bytes.NewBufferString("replacement"), 11, minio.PutObjectOptions{})
	require.NoError(t, err)
	repeated := api.Get("/iam/audit/archives/"+completed.ID.String()+"/download", "Authorization: Bearer test", "X-Tenant-Id: 11")
	require.Equal(t, http.StatusOK, repeated.Code, repeated.Body.String())
	assert.Equal(t, download.Body.Bytes(), repeated.Body.Bytes())
}
