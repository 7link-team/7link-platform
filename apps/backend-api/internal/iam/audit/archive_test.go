package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func archiveSnapshotFixture(t *testing.T) (*auditHarness, ExportSnapshot) {
	t.Helper()
	h := newAuditHarness(t, "sl_")
	for _, tenant := range []guid.ID{11, 11, 12} {
		_, err := h.service.Append(t.Context(), EventInput{
			TenantID: tenant, EventType: "archive_fixture", Outcome: "success",
			Detail: map[string]any{"value": "preserved"},
		})
		require.NoError(t, err)
	}
	snapshot, err := h.service.PrepareExport(t.Context(), Filter{TenantID: 11})
	require.NoError(t, err)
	return h, snapshot
}

func TestArchiveSnapshotWritesVerifiesAndReusesExactObject(t *testing.T) {
	h, snapshot := archiveSnapshotFixture(t)
	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "audit-archive")
	until := time.Now().UTC().AddDate(0, 0, 400)
	object, err := h.service.writeArchiveSnapshot(t.Context(), store, snapshot, 9001, until)
	require.NoError(t, err)
	assert.Equal(t, "audit/archives/11/9001.ndjson", object.Key)
	assert.Equal(t, store.bucket, object.Bucket)
	assert.NotEmpty(t, object.VersionID)
	assert.False(t, object.RetainUntil.Before(until))
	var expected bytes.Buffer
	require.NoError(t, h.service.WriteExport(t.Context(), snapshot, &expected))
	sum := sha256.Sum256(expected.Bytes())
	assert.Equal(t, hex.EncodeToString(sum[:]), object.SHA256)
	assert.Equal(t, int64(expected.Len()), object.Size)
	fixture.mu.Lock()
	assert.Equal(t, expected.Bytes(), fixture.objects[object.Key])
	assert.Equal(t, "COMPLIANCE", fixture.lockModes[object.Key])
	fixture.mu.Unlock()
	// A later append must neither enter the frozen export nor prevent recovery.
	_, err = h.service.Append(t.Context(), EventInput{TenantID: 11, EventType: "later", Outcome: "success"})
	require.NoError(t, err)
	repeated, err := h.service.writeArchiveSnapshot(t.Context(), store, snapshot, 9001, until)
	require.NoError(t, err)
	assert.Equal(t, object, repeated)
	fixture.mu.Lock()
	assert.Equal(t, 1, countArchivePuts(fixture.requestPaths))
	assert.Contains(t, fixture.requestPaths, "GET /audit-archive/"+object.Key+"?versionId="+object.VersionID)
	fixture.mu.Unlock()
	assert.EqualValues(t, 3, h.count(t, "iam_audit_events", 11))
	assert.EqualValues(t, 1, h.count(t, "iam_audit_events", 12))
}

func countArchivePuts(paths []string) int {
	count := 0
	for _, path := range paths {
		if strings.HasPrefix(path, "PUT ") && strings.Contains(path, "/audit/archives/") {
			count++
		}
	}
	return count
}

func TestArchiveSnapshotFailsClosedOnContentVersionAndRetention(t *testing.T) {
	for name, corrupt := range map[string]func(*fakeS3, ArchiveObject){
		"changed bytes same size":   func(f *fakeS3, o ArchiveObject) { f.objects[o.Key][1] ^= 1 },
		"truncated":                 func(f *fakeS3, o ArchiveObject) { f.objects[o.Key] = f.objects[o.Key][:10] },
		"missing version":           func(f *fakeS3, o ArchiveObject) { f.versions[o.Key] = "" },
		"null version":              func(f *fakeS3, o ArchiveObject) { f.versions[o.Key] = "null" },
		"governance not compliance": func(f *fakeS3, o ArchiveObject) { f.lockModes[o.Key] = "GOVERNANCE" },
		"short retention":           func(f *fakeS3, o ArchiveObject) { f.retention[o.Key] = time.Now().UTC().Format(time.RFC3339) },
	} {
		t.Run(name, func(t *testing.T) {
			h, snapshot := archiveSnapshotFixture(t)
			fixture := newFakeS3()
			store, _ := fakeAnchorStore(t, fixture, "audit-corruption")
			until := time.Now().UTC().Add(365 * 24 * time.Hour)
			object, err := h.service.writeArchiveSnapshot(t.Context(), store, snapshot, 9010, until)
			require.NoError(t, err)
			fixture.mu.Lock()
			corrupt(fixture, object)
			fixture.mu.Unlock()
			_, err = h.service.writeArchiveSnapshot(t.Context(), store, snapshot, 9010, until)
			assert.ErrorIs(t, err, ErrArchiveIntegrity)
			fixture.mu.Lock()
			assert.Equal(t, 1, countArchivePuts(fixture.requestPaths), "never replace a conflicting immutable artifact")
			fixture.mu.Unlock()
		})
	}
}

func TestArchiveSnapshotRetriesStorageFailures(t *testing.T) {
	for _, stage := range []struct{ method, contains string }{
		{http.MethodHead, "audit-retry"},
		{http.MethodPut, "_lock-probe"},
		{http.MethodPut, ".ndjson"},
		{http.MethodGet, ".ndjson"},
		{http.MethodGet, "retention"},
	} {
		t.Run(stage.method+stage.contains, func(t *testing.T) {
			h, snapshot := archiveSnapshotFixture(t)
			fixture := newFakeS3()
			fixture.failMethod, fixture.failContains = stage.method, stage.contains
			store, _ := fakeAnchorStore(t, fixture, "audit-retry")
			until := time.Now().UTC().Add(24 * time.Hour)
			_, err := h.service.writeArchiveSnapshot(t.Context(), store, snapshot, 9020, until)
			assert.ErrorIs(t, err, ErrArchiveUnavailable)
			fixture.mu.Lock()
			fixture.failMethod, fixture.failContains = "", ""
			fixture.mu.Unlock()
			_, err = h.service.writeArchiveSnapshot(t.Context(), store, snapshot, 9020, until)
			require.NoError(t, err, "same client must recover without restarting the process")
		})
	}
}

func TestArchiveSnapshotConcurrentReplicasConverge(t *testing.T) {
	h, snapshot := archiveSnapshotFixture(t)
	fixture := newFakeS3()
	store, server := fakeAnchorStore(t, fixture, "audit-replicas")
	other, err := NewAnchorStore(AnchorConfig{
		Endpoint: server.URL, Bucket: store.bucket, AccessKey: "fixture", SecretKey: "fixture", RetentionDays: 365,
	})
	require.NoError(t, err)
	until := time.Now().UTC().Add(24 * time.Hour)
	start := make(chan struct{})
	results := make(chan ArchiveObject, 2)
	failures := make(chan error, 2)
	var workers sync.WaitGroup
	for _, replica := range []*AnchorStore{store, other} {
		workers.Go(func() {
			<-start
			result, runErr := h.service.writeArchiveSnapshot(t.Context(), replica, snapshot, 9030, until)
			results <- result
			failures <- runErr
		})
	}
	close(start)
	workers.Wait()
	require.NoError(t, <-failures)
	require.NoError(t, <-failures)
	first, second := <-results, <-results
	assert.Equal(t, first, second)
}

func TestArchiveSnapshotRejectsInvalidAndChangedSource(t *testing.T) {
	h, snapshot := archiveSnapshotFixture(t)
	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "audit-source")
	until := time.Now().UTC().Add(24 * time.Hour)
	for _, mutate := range []func(*ExportSnapshot){
		func(s *ExportSnapshot) { s.Filter.TenantID = 0 },
		func(s *ExportSnapshot) { s.Filter.TenantID = 12 },
		func(s *ExportSnapshot) { s.Integrity.Valid = false },
		func(s *ExportSnapshot) { s.Integrity.HeadSequence = 0 },
		func(s *ExportSnapshot) { s.GeneratedAt = time.Time{} },
		func(s *ExportSnapshot) { s.Filter.EventType = "silently_omit_rows" },
		func(s *ExportSnapshot) { s.Filter.To = auditTestNow },
		func(s *ExportSnapshot) { s.Filter.Offset = 1 },
	} {
		invalid := snapshot
		mutate(&invalid)
		_, err := h.service.writeArchiveSnapshot(t.Context(), store, invalid, 9040, until)
		assert.ErrorIs(t, err, ErrArchiveInvalid)
	}
	invalid := snapshot
	invalid.Integrity.HeadHash = strings.Repeat("0", 64)
	_, err := h.service.writeArchiveSnapshot(t.Context(), store, invalid, 9040, until)
	assert.ErrorIs(t, err, ErrIntegrity)
	_, err = h.service.writeArchiveSnapshot(t.Context(), nil, snapshot, 9040, until)
	assert.ErrorIs(t, err, ErrArchiveInvalid)
	_, err = (*Service)(nil).writeArchiveSnapshot(t.Context(), store, snapshot, 9040, until)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = h.service.writeArchiveSnapshot(canceled, store, snapshot, 9040, until)
	assert.ErrorIs(t, err, context.Canceled)
	fixture.mu.Lock()
	assert.Empty(t, fixture.requestPaths)
	fixture.mu.Unlock()
}

func TestArchiveUploadRejectsChangedStreamingSourceAndWriterFailures(t *testing.T) {
	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "audit-stream")
	payload := []byte("original")
	sum := sha256.Sum256(payload)
	expected := ArchiveObject{
		Bucket: store.bucket, Key: archivePrefix + "/11/9050.ndjson", Size: int64(len(payload)),
		SHA256: hex.EncodeToString(sum[:]), RetainUntil: time.Now().UTC().Add(time.Hour),
	}
	_, err := store.putArchive(t.Context(), expected, func(w io.Writer) error {
		_, writeErr := w.Write([]byte("modified"))
		return writeErr
	})
	require.Error(t, err)
	// Source failures must terminate the pipe producer even if upload stops early.
	expected.Key = archivePrefix + "/11/9051.ndjson"
	_, err = store.putArchive(t.Context(), expected, func(io.Writer) error { return errors.New("source failed") })
	require.Error(t, err)
	count := &archiveCountingWriter{writer: archiveShortWriter{}}
	_, err = count.Write(payload)
	assert.ErrorIs(t, err, io.ErrShortWrite)
}

type archiveShortWriter struct{}

func (archiveShortWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestArchiveMultipartProtocolAndFailureCleanup(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), archiveSinglePutSize+1)
	sourceFailure := errors.New("source failed after its declared length")
	for _, scenario := range []struct {
		name       string
		failure    string
		abortCode  string
		wantAbort  bool
		wantError  bool
		wantSource bool
	}{
		{name: "complete"},
		{name: "initiation denied", failure: "init", wantError: true},
		{name: "part denied", failure: "part", wantAbort: true, wantError: true},
		{name: "completion denied", failure: "complete", wantAbort: true, wantError: true},
		{name: "conditional loser", failure: "conflict", wantAbort: true, wantError: true},
		{name: "abort denied", failure: "part", abortCode: "AccessDenied", wantAbort: true, wantError: true},
		{name: "already aborted", failure: "part", abortCode: "NoSuchUpload", wantAbort: true, wantError: true},
		{name: "extra source bytes", failure: "extra", wantAbort: true, wantError: true},
		{name: "source failed", failure: "source", wantAbort: true, wantError: true, wantSource: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var aborted, uploaded, completed atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				switch {
				case query.Has("location"):
					_, _ = io.WriteString(w, `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></LocationConstraint>`)
				case r.Method == http.MethodPost && query.Has("uploads"):
					if scenario.failure == "init" {
						fakeS3Error(w, http.StatusForbidden, "AccessDenied", "initiation denied")
						return
					}
					assert.Equal(t, "COMPLIANCE", r.Header.Get("X-Amz-Object-Lock-Mode"))
					assert.NotEmpty(t, r.Header.Get("X-Amz-Object-Lock-Retain-Until-Date"))
					_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>archive-protocol</Bucket><Key>archive.ndjson</Key><UploadId>fixture-upload</UploadId></InitiateMultipartUploadResult>`)
				case r.Method == http.MethodPut && query.Has("uploadId"):
					_, readErr := io.Copy(io.Discard, r.Body)
					assert.NoError(t, readErr)
					uploaded.Add(1)
					if scenario.failure == "part" {
						fakeS3Error(w, http.StatusForbidden, "AccessDenied", "part denied")
						return
					}
					w.Header().Set("ETag", `"part-etag"`)
				case r.Method == http.MethodPost && query.Has("uploadId"):
					completed.Add(1)
					assert.Equal(t, "*", r.Header.Get("If-None-Match"))
					assert.Empty(t, r.Header.Get("X-Amz-Object-Lock-Mode"))
					assert.EqualValues(t, 2, uploaded.Load())
					if scenario.failure == "conflict" {
						fakeS3Error(w, http.StatusPreconditionFailed, "PreconditionFailed", "already published")
						return
					}
					if scenario.failure == "complete" {
						fakeS3Error(w, http.StatusForbidden, "AccessDenied", "completion denied")
						return
					}
					w.Header().Set("X-Amz-Version-Id", "immutable-version")
					_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><Bucket>archive-protocol</Bucket><Key>archive.ndjson</Key><ETag>whole-etag-2</ETag></CompleteMultipartUploadResult>`)
				case r.Method == http.MethodDelete && query.Has("uploadId"):
					aborted.Add(1)
					assert.NoError(t, r.Context().Err())
					if scenario.abortCode != "" {
						fakeS3Error(w, http.StatusForbidden, scenario.abortCode, "abort response")
						return
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected archive request: %s", r.Method)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			store, err := NewAnchorStore(AnchorConfig{Endpoint: server.URL, Bucket: "archive-protocol", AccessKey: "fixture", SecretKey: "fixture", RetentionDays: 1})
			require.NoError(t, err)
			opts := minio.PutObjectOptions{Mode: minio.Compliance, RetainUntilDate: time.Now().UTC().Add(time.Hour)}
			opts.SetMatchETagExcept("*")
			var reader io.Reader = bytes.NewReader(payload)
			if scenario.failure == "extra" {
				reader = io.MultiReader(reader, strings.NewReader("extra"))
			}
			if scenario.wantSource {
				reader = io.MultiReader(reader, iotest.ErrReader(sourceFailure))
			}
			result, err := store.uploadArchive(t.Context(), "archive.ndjson", reader, int64(len(payload)), opts)
			if scenario.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "immutable-version", result.VersionID)
				assert.EqualValues(t, len(payload), result.Size)
			}
			if scenario.wantSource {
				assert.ErrorIs(t, err, sourceFailure)
				assert.Zero(t, completed.Load())
			}
			if scenario.failure == "extra" {
				assert.ErrorIs(t, err, ErrArchiveIntegrity)
				assert.Zero(t, completed.Load())
			}
			if scenario.failure == "conflict" {
				assert.Equal(t, "PreconditionFailed", minio.ToErrorResponse(err).Code)
			}
			if scenario.abortCode == "AccessDenied" {
				assert.ErrorContains(t, err, "abort archive multipart upload")
			}
			assert.Equal(t, scenario.wantAbort, aborted.Load() == 1)
		})
	}
	_, err := (&AnchorStore{}).uploadArchive(t.Context(), "oversized", bytes.NewReader(nil), 1<<60, minio.PutObjectOptions{})
	assert.Error(t, err)
}

func TestArchiveRealObjectLockAndRecovery(t *testing.T) {
	endpoint := os.Getenv("IAM_ARCHIVE_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("set IAM_ARCHIVE_MINIO_ENDPOINT to an isolated MinIO fixture")
	}
	h, snapshot := archiveSnapshotFixture(t)
	config := AnchorConfig{
		Endpoint: endpoint, Bucket: "audit-archive-real-" + time.Now().UTC().Format("20060102-150405.000000000"),
		AccessKey: auditMinIOTestUser, SecretKey: auditMinIOTestPassword, RetentionDays: 1,
	}
	store, err := NewAnchorStore(config)
	require.NoError(t, err)
	until := time.Now().UTC().Add(24 * time.Hour)
	object, err := h.service.writeArchiveSnapshot(t.Context(), store, snapshot, 9060, until)
	require.NoError(t, err)
	require.NotEmpty(t, object.VersionID)
	restarted, err := NewAnchorStore(config)
	require.NoError(t, err)
	recovered, err := h.service.writeArchiveSnapshot(t.Context(), restarted, snapshot, 9060, until)
	require.NoError(t, err)
	assert.Equal(t, object, recovered)
	err = store.client.RemoveObject(t.Context(), object.Bucket, object.Key, minio.RemoveObjectOptions{VersionID: object.VersionID})
	require.Error(t, err, "the actual archived version must refuse deletion")
	_, err = store.verifyArchive(t.Context(), object)
	require.NoError(t, err)
	// An unrelated latest version cannot change the persisted archive reference.
	_, err = store.client.PutObject(t.Context(), object.Bucket, object.Key, strings.NewReader("replacement"), 11, minio.PutObjectOptions{})
	require.NoError(t, err)
	_, err = store.verifyArchive(t.Context(), object)
	require.NoError(t, err)
	_, err = h.service.writeArchiveSnapshot(t.Context(), restarted, snapshot, 9060, until)
	assert.ErrorIs(t, err, ErrArchiveIntegrity, "a conflicting latest version must not be accepted on retry")
	assert.EqualValues(t, 2, h.count(t, "iam_audit_events", 11))
}
