package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func archiveJobFixture(t *testing.T) (*auditHarness, *fakeS3) {
	t.Helper()
	h, _ := archiveSnapshotFixture(t)
	db, err := h.db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = h.prefix
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, "sqlite", 3))
	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "audit-jobs")
	h.service.archive = store
	h.service.now = func() time.Time { return auditTestNow.AddDate(0, 0, 731) }
	return h, fixture
}

func TestArchiveJobsPersistRequestExecutionAndTenantIsolation(t *testing.T) {
	h, fixture := archiveJobFixture(t)
	job, err := h.service.RequestArchive(t.Context(), 11)
	require.NoError(t, err)
	assert.Equal(t, "queued", job.State)
	assert.EqualValues(t, 2, job.HeadSequence)
	assert.Zero(t, job.AfterSequence)
	assert.Nil(t, job.Object)
	again, err := h.service.RequestArchive(t.Context(), 11)
	require.NoError(t, err)
	assert.Equal(t, job, again)
	_, err = h.service.RunArchive(t.Context(), 12, job.ID, time.Minute)
	assert.ErrorIs(t, err, ErrArchiveNotFound)
	fixture.mu.Lock()
	assert.Empty(t, fixture.requestPaths)
	fixture.mu.Unlock()
	// Reconstruct service state entirely from the database.
	restarted := NewService(h.runtime, h.service.repo.ids)
	restarted.archive, restarted.now = h.service.archive, h.service.now
	completed, err := restarted.RunArchive(t.Context(), 11, job.ID, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "complete", completed.State)
	assert.EqualValues(t, 1, completed.Attempts)
	require.NotNil(t, completed.Object)
	require.NotEmpty(t, completed.Object.VersionID)
	require.NotEmpty(t, completed.Object.SHA256)
	listed, err := restarted.ListArchiveJobs(t.Context(), 11, 0, 20)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, completed, listed[0])
	foreign, err := restarted.ListArchiveJobs(t.Context(), 12, 0, 20)
	require.NoError(t, err)
	assert.Empty(t, foreign)
	again, err = restarted.RunArchive(t.Context(), 11, job.ID, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, completed, again)
	_, err = restarted.RequestArchive(t.Context(), 11)
	assert.ErrorIs(t, err, ErrArchiveEmpty, "recent archive lifecycle events are not old enough")
	assert.EqualValues(t, 4, h.count(t, "iam_audit_events", 11))
	integrity, err := restarted.Verify(t.Context(), 11)
	require.NoError(t, err)
	assert.True(t, integrity.Valid)
}

func TestArchiveJobsPersistFailureBackoffAndRecoverUpload(t *testing.T) {
	h, fixture := archiveJobFixture(t)
	job, err := h.service.RequestArchive(t.Context(), 11)
	require.NoError(t, err)
	fixture.mu.Lock()
	fixture.failMethod, fixture.failContains = http.MethodGet, "retention"
	fixture.mu.Unlock()
	_, err = h.service.RunArchive(t.Context(), 11, job.ID, time.Minute)
	assert.ErrorIs(t, err, ErrArchiveUnavailable)
	jobs, err := h.service.ListArchiveJobs(t.Context(), 11, 0, 20)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "failed", jobs[0].State)
	assert.Equal(t, "storage_unavailable", jobs[0].ErrorCode)
	assert.EqualValues(t, 1, jobs[0].Attempts)
	assert.Greater(t, jobs[0].NextAttempt, time.Now().UnixMilli())
	assert.Nil(t, jobs[0].Object)
	_, err = h.service.RunArchive(t.Context(), 11, job.ID, time.Minute)
	assert.ErrorIs(t, err, ErrArchiveBusy)
	fixture.mu.Lock()
	fixture.failMethod, fixture.failContains = "", ""
	fixture.mu.Unlock()
	require.NoError(t, h.db.Table(h.table("iam_audit_archive_jobs")).Where("id = ?", job.ID).Update("next_attempt", 0).Error)
	completed, err := h.service.RunArchive(t.Context(), 11, job.ID, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "complete", completed.State)
	assert.EqualValues(t, 2, completed.Attempts)
	assert.Empty(t, completed.ErrorCode)
	fixture.mu.Lock()
	assert.Equal(t, 1, countArchivePuts(fixture.requestPaths), "lost verification must not create another version")
	fixture.mu.Unlock()
}

func TestArchiveJobsFenceStaleCompletionAndReclaimExpiredLease(t *testing.T) {
	h, _ := archiveJobFixture(t)
	job, err := h.service.RequestArchive(t.Context(), 11)
	require.NoError(t, err)
	table := h.table("iam_audit_archive_jobs")
	require.NoError(t, h.db.Table(table).Where("id = ?", job.ID).Updates(map[string]any{
		"state": "running", "attempts": 1, "lease_token": 777, "lease_until": time.Now().Add(time.Minute).UnixMilli(),
	}).Error)
	stale, err := h.service.repo.archiveJob(t.Context(), 11, job.ID)
	require.NoError(t, err)
	_, err = h.service.RunArchive(t.Context(), 11, job.ID, time.Minute)
	assert.ErrorIs(t, err, ErrArchiveBusy)
	require.NoError(t, h.db.Table(table).Where("id = ?", job.ID).Updates(map[string]any{"lease_token": 778, "lease_until": 0}).Error)
	err = h.service.finishArchive(t.Context(), stale, ArchiveObject{}, errors.New("old worker"))
	assert.ErrorIs(t, err, ErrArchiveBusy)
	completed, err := h.service.RunArchive(t.Context(), 11, job.ID, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "complete", completed.State)
	assert.EqualValues(t, 2, completed.Attempts)
	err = h.service.finishArchive(t.Context(), stale, ArchiveObject{}, errors.New("old worker"))
	assert.ErrorIs(t, err, ErrArchiveBusy)
}

func TestArchiveJobsIncrementalSegmentsPreserveChainAndRetentionBoundary(t *testing.T) {
	h, fixture := archiveJobFixture(t)
	h.service.now = func() time.Time { return auditTestNow }
	for i := 2; i < exportBatchSize+1; i++ {
		_, err := h.service.Append(t.Context(), EventInput{TenantID: 11, EventType: "segment", Outcome: "success"})
		require.NoError(t, err)
	}
	// At exactly archive_after_days old, the strict cutoff must not include rows.
	h.service.now = func() time.Time { return auditTestNow.AddDate(0, 0, 730) }
	_, err := h.service.RequestArchive(t.Context(), 11)
	assert.ErrorIs(t, err, ErrArchiveEmpty)
	h.service.now = func() time.Time { return auditTestNow.AddDate(0, 0, 731) }
	first, err := h.service.RequestArchive(t.Context(), 11)
	require.NoError(t, err)
	assert.EqualValues(t, exportBatchSize, first.HeadSequence)
	first, err = h.service.RunArchive(t.Context(), 11, first.ID, time.Minute)
	require.NoError(t, err)
	second, err := h.service.RequestArchive(t.Context(), 11)
	require.NoError(t, err)
	assert.EqualValues(t, exportBatchSize, second.AfterSequence)
	assert.EqualValues(t, exportBatchSize+1, second.HeadSequence)
	second, err = h.service.RunArchive(t.Context(), 11, second.ID, time.Minute)
	require.NoError(t, err)
	fixture.mu.Lock()
	payload := bytes.Clone(fixture.objects[second.Object.Key])
	fixture.mu.Unlock()
	lines := bytes.Split(bytes.TrimSpace(payload), []byte{'\n'})
	require.Len(t, lines, 3)
	var manifest exportManifest
	var event exportEvent
	var complete exportComplete
	require.NoError(t, json.Unmarshal(lines[0], &manifest))
	require.NoError(t, json.Unmarshal(lines[1], &event))
	require.NoError(t, json.Unmarshal(lines[2], &complete))
	assert.EqualValues(t, exportBatchSize, manifest.AfterSequence)
	assert.EqualValues(t, exportBatchSize+1, event.Event.Sequence)
	assert.Equal(t, manifest.PreviousHash, event.Event.PreviousHash)
	assert.EqualValues(t, 1, complete.ExportedEvents)
	assert.EqualValues(t, exportBatchSize+5, h.count(t, "iam_audit_events", 11))
}

func TestArchiveJobsRejectInvalidRequestsAndCorruptPersistedSnapshots(t *testing.T) {
	h, _ := archiveJobFixture(t)
	_, err := (*Service)(nil).RequestArchive(t.Context(), 11)
	assert.ErrorIs(t, err, ErrArchiveDisabled)
	_, err = h.service.RequestArchive(t.Context(), 0)
	assert.ErrorIs(t, err, ErrArchiveInvalid)
	_, err = h.service.ListArchiveJobs(t.Context(), 0, 0, 20)
	assert.ErrorIs(t, err, ErrArchiveInvalid)
	_, err = h.service.ListArchiveJobs(t.Context(), 11, -1, 20)
	assert.ErrorIs(t, err, ErrArchiveInvalid)
	_, err = h.service.RunArchive(t.Context(), 11, 1, 0)
	assert.ErrorIs(t, err, ErrArchiveInvalid)
	job, err := h.service.RequestArchive(t.Context(), 11)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.table("iam_audit_archive_jobs")).Where("id = ?", job.ID).Update("snapshot", "{}").Error)
	_, err = h.service.RunArchive(t.Context(), 11, job.ID, time.Minute)
	assert.ErrorIs(t, err, ErrArchiveIntegrity)
	jobs, err := h.service.ListArchiveJobs(t.Context(), 11, 0, 20)
	require.NoError(t, err)
	assert.Equal(t, "integrity_failed", jobs[0].ErrorCode)
	_, err = archiveJobView(model.ArchiveJob{State: "complete", Object: "{}"})
	assert.ErrorIs(t, err, ErrArchiveIntegrity)
}
