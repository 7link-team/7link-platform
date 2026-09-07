package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The app browser harness invokes this only on a new, isolated SQLite file.
// Seeding before app bootstrap keeps old events at the start of each chain.
func TestArchiveBrowserSeed(t *testing.T) {
	database := os.Getenv("IAM_ARCHIVE_BROWSER_DATABASE")
	if database == "" {
		t.Skip("invoked by the isolated archive browser harness")
	}
	require.True(t, filepath.IsAbs(database))
	require.Equal(t, "archive-browser.sqlite", filepath.Base(database))
	file, err := os.OpenFile(database, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	require.NoError(t, err, "never seed an existing database")
	require.NoError(t, file.Close())
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: "sqlite", DSN: database, Writable: true, Readable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	db, err := router.Write().DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), db, (&Module{}).MigrationSource(), "sqlite"))
	runtime := appenv.NewRuntime(appenv.NewIdentity("archive-browser-fixture", "1"), appenv.WithDatabase(router))
	service := NewService(runtime, newAuditTestIDs(1000))
	service.now = func() time.Time { return time.Now().UTC().AddDate(0, 0, -800) }
	for tenant, count := range map[guid.ID]int{0: 1, 91001: exportBatchSize + 1, 91002: 2} {
		for range count {
			_, err := service.Append(t.Context(), EventInput{TenantID: tenant, EventType: "archive_browser_fixture", Outcome: "success"})
			require.NoError(t, err)
		}
		integrity, err := service.Verify(t.Context(), tenant)
		require.NoError(t, err)
		require.True(t, integrity.Valid)
	}
}

// The lifecycle harness owns these isolated databases. Every service opens its
// own pool so claims and recovery cannot accidentally rely on process-local state.
func TestArchiveExternalDatabase(t *testing.T) {
	dialect, dsn := os.Getenv("IAM_LIFECYCLE_DATABASE_TYPE"), os.Getenv("IAM_LIFECYCLE_DATABASE_DSN")
	if dialect == "" && dsn == "" {
		t.Skip("external database is provided by lifecycle and real MySQL gates")
	}
	require.NotEmpty(t, dialect)
	require.NotEmpty(t, dsn)
	prefix := os.Getenv("IAM_LIFECYCLE_TABLE_PREFIX") + "archive_"
	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "archive-multi-pool")
	open := func(firstID int64) *Service {
		router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
			"primary": {Type: dialect, DSN: dsn, Writable: true, Readable: true},
		})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, router.Close()) })
		runtime := appenv.NewRuntime(appenv.NewIdentity("archive-integration", "1"),
			appenv.WithDatabase(router), appenv.WithTablePrefix(prefix))
		service := NewService(runtime, newAuditTestIDs(firstID))
		service.archive = store
		return service
	}
	first, second := open(1000), open(100000)
	sqlDB, err := first.repo.writer().DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = prefix
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, dialect))
	first.now = func() time.Time { return time.Now().UTC().AddDate(0, 0, -800) }
	for _, tenant := range []guid.ID{0, 11, 11, 12} {
		_, err := first.Append(t.Context(), EventInput{TenantID: tenant, EventType: "old_event", Outcome: "success"})
		require.NoError(t, err)
	}
	first.now = time.Now
	services := []*Service{first, second}
	var group sync.WaitGroup
	results := make(chan ArchiveJob, 16)
	failures := make(chan error, 16)
	start := make(chan struct{})
	for i := range 16 {
		group.Go(func() {
			<-start
			job, err := services[i%2].RequestArchive(t.Context(), 11)
			results <- job
			failures <- err
		})
	}
	close(start)
	group.Wait()
	var id guid.ID
	for range 16 {
		require.NoError(t, <-failures)
		job := <-results
		if id.Zero() {
			id = job.ID
		}
		assert.Equal(t, id, job.ID, "concurrent requests must converge on one snapshot")
		assert.EqualValues(t, 2, job.HeadSequence)
	}
	count, err := first.repo.archiveJobCount(t.Context(), 11)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
	for i := range 16 {
		group.Go(func() {
			_, runErr := services[i%2].RunArchive(t.Context(), 11, id, time.Minute)
			failures <- runErr
		})
	}
	group.Wait()
	for range 16 {
		err := <-failures
		assert.True(t, err == nil || errors.Is(err, ErrArchiveBusy), "unexpected claim result: %v", err)
	}
	jobs, err := second.ListArchiveJobs(t.Context(), 11, 0, 20)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "complete", jobs[0].State)
	assert.EqualValues(t, 1, jobs[0].Attempts, "exactly one leased upload")
	fixture.mu.Lock()
	assert.Equal(t, 1, countArchivePuts(fixture.requestPaths))
	fixture.mu.Unlock()
	status, err := first.archiveStatus(t.Context(), 11)
	require.NoError(t, err)
	assert.EqualValues(t, 2, status.ArchivedEvents)
	assert.Zero(t, status.PendingEvents)
	assert.EqualValues(t, 4, status.TotalEvents, "source rows plus one request and one completion")

	// Model a crash after S3 committed but before the database acknowledged it.
	job, err := first.RequestArchive(t.Context(), 12)
	require.NoError(t, err)
	table := prefix + "iam_audit_archive_jobs"
	require.NoError(t, first.repo.writer().Table(table).Where("id = ?", job.ID).
		Updates(map[string]any{"state": "running", "attempts": 1, "lease_token": 777, "lease_until": 0}).Error)
	stale, err := first.repo.archiveJob(t.Context(), 12, job.ID)
	require.NoError(t, err)
	var snapshot ExportSnapshot
	require.NoError(t, json.Unmarshal([]byte(stale.Snapshot), &snapshot))
	object, err := first.writeArchiveSnapshot(t.Context(), store, snapshot, job.ID, job.RetainUntil)
	require.NoError(t, err)
	restarted := open(200000)
	_, err = restarted.retryArchive(t.Context(), 12, job.ID)
	require.NoError(t, err)
	recovered, err := restarted.RunArchive(t.Context(), 12, job.ID, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, recovered.Object)
	assert.Equal(t, object, *recovered.Object)
	assert.EqualValues(t, 2, recovered.Attempts)
	assert.ErrorIs(t, first.finishArchive(t.Context(), stale, object, nil), ErrArchiveBusy)
	_, err = restarted.verifyArchiveJob(t.Context(), 11, job.ID)
	assert.ErrorIs(t, err, ErrArchiveNotFound)
	for _, tenant := range []guid.ID{11, 12} {
		integrity, err := restarted.Verify(t.Context(), tenant)
		require.NoError(t, err)
		assert.True(t, integrity.Valid)
	}
	require.NoError(t, restarted.sweepArchives(t.Context(), time.Minute))
	platform, err := restarted.archiveStatus(t.Context(), 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, platform.ArchivedEvents)
	assert.EqualValues(t, 1, platform.CompletedJobs)
}

type archiveMultipartBarrier struct {
	test        *testing.T
	base        http.RoundTripper
	path        string
	missing     atomic.Int32
	completions atomic.Int32
	ready       chan struct{}
}

func (b *archiveMultipartBarrier) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path == b.path && request.Method == http.MethodPost && request.URL.Query().Has("uploadId") {
		b.completions.Add(1)
	}
	response, err := b.base.RoundTrip(request)
	if err == nil && response.StatusCode >= 400 && request.Method != http.MethodHead {
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(body))
		var failure minio.ErrorResponse
		if readErr == nil && xml.Unmarshal(body, &failure) == nil {
			b.test.Logf("multipart %s response %d: %s: %s", request.Method, response.StatusCode, failure.Code, failure.Message)
		}
	}
	if err == nil && request.Method == http.MethodHead && request.URL.Path == b.path && response.StatusCode == http.StatusNotFound {
		n := b.missing.Add(1)
		if n == 2 {
			close(b.ready)
		}
		if n <= 2 {
			select {
			case <-b.ready:
			case <-request.Context().Done():
				_ = response.Body.Close()
				return nil, request.Context().Err()
			}
		}
	}
	return response, err
}

func TestArchiveRealMultipartReplicasConverge(t *testing.T) {
	endpoint := os.Getenv("IAM_ARCHIVE_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("set IAM_ARCHIVE_MINIO_ENDPOINT to an isolated MinIO fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	bucket := "audit-multipart-" + time.Now().UTC().Format("20060102-150405.000000000")
	config := AnchorConfig{Endpoint: endpoint, Bucket: bucket, AccessKey: auditMinIOTestUser, SecretKey: auditMinIOTestPassword, RetentionDays: 1}
	transport, err := minio.DefaultTransport(false)
	require.NoError(t, err)
	t.Cleanup(transport.CloseIdleConnections)
	key := "audit/archives/11/9070.ndjson"
	barrier := &archiveMultipartBarrier{test: t, base: transport, path: "/" + bucket + "/" + key, ready: make(chan struct{})}
	stores := make([]*AnchorStore, 2)
	for i := range stores {
		stores[i], err = NewAnchorStore(config)
		require.NoError(t, err)
		address, secure, parseErr := endpointAddress(endpoint)
		require.NoError(t, parseErr)
		stores[i].client, err = minio.New(address, &minio.Options{Secure: secure, Transport: barrier,
			Creds: credentials.NewStaticV4(auditMinIOTestUser, auditMinIOTestPassword, "")})
		require.NoError(t, err)
		require.NoError(t, stores[i].ensureBucket(ctx))
	}
	// A bounded 1 MiB buffer produces >64 MiB, forcing multipart upload.
	line := []byte(`{"fixture":"` + strings.Repeat("x", 1<<20) + "\"}\n")
	write := func(writer io.Writer) error {
		for range 70 {
			if _, err := io.Copy(writer, bytes.NewReader(line)); err != nil {
				return err
			}
		}
		return nil
	}
	digest := sha256.New()
	require.NoError(t, write(digest))
	expected := ArchiveObject{Bucket: bucket, Key: key, Size: int64(70 * len(line)), SHA256: hex.EncodeToString(digest.Sum(nil)), RetainUntil: time.Now().UTC().Add(time.Hour)}
	var results [2]ArchiveObject
	var failures [2]error
	var group sync.WaitGroup
	for i := range stores {
		group.Go(func() { results[i], failures[i] = stores[i].putArchive(ctx, expected, write) })
	}
	group.Wait()
	require.NoError(t, failures[0])
	require.NoError(t, failures[1])
	assert.Equal(t, results[0], results[1], "concurrent multipart completion must converge on one immutable version")
	assert.GreaterOrEqual(t, barrier.completions.Load(), int32(2), "both replicas reached actual multipart completion")
	info, err := stores[0].client.StatObject(ctx, bucket, key, minio.StatObjectOptions{VersionID: results[0].VersionID})
	require.NoError(t, err)
	assert.Regexp(t, `-[2-9][0-9]*$`, info.ETag)
	versions := 0
	for object := range stores[0].client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: key, WithVersions: true, Recursive: true}) {
		require.NoError(t, object.Err)
		if object.Key == key {
			versions++
		}
	}
	assert.Equal(t, 1, versions, "the losing completion must not publish another version")
	for upload := range stores[0].client.ListIncompleteUploads(ctx, bucket, key, true) {
		require.NoError(t, upload.Err)
		assert.NotEqual(t, key, upload.Key, "the losing replica must abort its parts")
	}
}

type archiveTestTransport func(*http.Request) (*http.Response, error)

func (f archiveTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestArchiveRealMultipartFailureRecovery(t *testing.T) {
	endpoint := os.Getenv("IAM_ARCHIVE_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("set IAM_ARCHIVE_MINIO_ENDPOINT to an isolated MinIO fixture")
	}
	block := bytes.Repeat([]byte("x"), 1<<20)
	write := func(w io.Writer) error {
		for range 18 {
			if _, err := w.Write(block); err != nil {
				return err
			}
		}
		return nil
	}
	digest := sha256.New()
	require.NoError(t, write(digest))
	for _, scenario := range []string{"cancel", "source failure", "changed source", "lost completion"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			bucket := "audit-recovery-" + time.Now().UTC().Format("20060102-150405.000000000")
			store, err := NewAnchorStore(AnchorConfig{Endpoint: endpoint, Bucket: bucket, AccessKey: auditMinIOTestUser, SecretKey: auditMinIOTestPassword, RetentionDays: 1})
			require.NoError(t, err)
			address, secure, err := endpointAddress(endpoint)
			require.NoError(t, err)
			base, err := minio.DefaultTransport(secure)
			require.NoError(t, err)
			t.Cleanup(base.CloseIdleConnections)
			var fault atomic.Bool
			fault.Store(true)
			var completedParts atomic.Int32
			transport := archiveTestTransport(func(r *http.Request) (*http.Response, error) {
				if scenario == "cancel" && r.Method == http.MethodPut && r.URL.Query().Get("partNumber") == "2" && fault.Swap(false) {
					cancel()
					return nil, ctx.Err()
				}
				response, sendErr := base.RoundTrip(r)
				if sendErr == nil && response.StatusCode == http.StatusOK {
					if r.Method == http.MethodPut && r.URL.Query().Has("partNumber") {
						completedParts.Add(1)
					}
					if scenario == "lost completion" && r.Method == http.MethodPost && r.URL.Query().Has("uploadId") && fault.Swap(false) {
						_, _ = io.Copy(io.Discard, response.Body)
						_ = response.Body.Close()
						return nil, errors.New("simulated lost completion response")
					}
				}
				return response, sendErr
			})
			store.client, err = minio.New(address, &minio.Options{Secure: secure, Transport: transport,
				Creds: credentials.NewStaticV4(auditMinIOTestUser, auditMinIOTestPassword, "")})
			require.NoError(t, err)
			expected := ArchiveObject{Bucket: bucket, Key: "audit/archives/11/9080.ndjson", Size: int64(18 * len(block)),
				SHA256: hex.EncodeToString(digest.Sum(nil)), RetainUntil: time.Now().UTC().Add(time.Hour)}
			attempt := expected
			if scenario == "changed source" {
				attempt.SHA256 = strings.Repeat("0", 64)
			}
			initial, err := store.putArchive(ctx, attempt, func(w io.Writer) error {
				if err := write(w); err != nil {
					return err
				}
				if scenario == "source failure" {
					return errors.New("source interrupted")
				}
				return nil
			})
			if scenario == "lost completion" {
				// The SDK retries completion, receives the conditional conflict,
				// and putArchive verifies the winner within the same attempt.
				require.NoError(t, err)
				assert.False(t, fault.Load(), "the actual successful response was lost")
			} else {
				require.Error(t, err)
			}
			if scenario == "cancel" {
				assert.ErrorIs(t, err, context.Canceled)
				assert.EqualValues(t, 1, completedParts.Load(), "cancel after a real part was committed")
			}
			if scenario != "lost completion" {
				_, statErr := store.client.StatObject(t.Context(), bucket, expected.Key, minio.StatObjectOptions{})
				assert.Equal(t, minio.NoSuchKey, minio.ToErrorResponse(statErr).Code, "failed producer must not publish an object")
			}
			for upload := range store.client.ListIncompleteUploads(t.Context(), bucket, expected.Key, true) {
				require.NoError(t, upload.Err)
				assert.NotEqual(t, expected.Key, upload.Key, "failure cleanup must survive cancellation")
			}
			recovered, err := store.putArchive(t.Context(), expected, write)
			require.NoError(t, err)
			assert.Equal(t, expected.SHA256, recovered.SHA256)
			if scenario == "lost completion" {
				assert.Equal(t, initial, recovered)
			}
			versions := 0
			for object := range store.client.ListObjects(t.Context(), bucket, minio.ListObjectsOptions{Prefix: expected.Key, WithVersions: true, Recursive: true}) {
				require.NoError(t, object.Err)
				if object.Key == expected.Key {
					versions++
				}
			}
			assert.Equal(t, 1, versions)
		})
	}
}

func TestArchiveRealSegmentsPreserveWholeChain(t *testing.T) {
	endpoint := os.Getenv("IAM_ARCHIVE_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("set IAM_ARCHIVE_MINIO_ENDPOINT to an isolated MinIO fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	h, _ := archiveJobFixture(t)
	store, err := NewAnchorStore(AnchorConfig{Endpoint: endpoint,
		Bucket:    "audit-segments-" + time.Now().UTC().Format("20060102-150405.000000000"),
		AccessKey: auditMinIOTestUser, SecretKey: auditMinIOTestPassword, RetentionDays: 1})
	require.NoError(t, err)
	h.service.archive = store
	h.service.now = func() time.Time { return auditTestNow }
	const eventCount = 2*exportBatchSize + 3
	detail := map[string]any{"fixture": strings.Repeat("x", 34<<10)}
	for i := 2; i < eventCount; i++ {
		_, err := h.service.Append(ctx, EventInput{TenantID: 11, EventType: "large_chain", Outcome: "success", Detail: detail})
		require.NoError(t, err)
	}
	h.service.now = func() time.Time { return auditTestNow.AddDate(0, 0, 731) }
	var sequence int64
	previousHash := ""
	for segment := range 3 {
		job, err := h.service.RequestArchive(ctx, 11)
		require.NoError(t, err)
		require.Equal(t, sequence, job.AfterSequence)
		job, err = h.service.RunArchive(ctx, 11, job.ID, time.Minute)
		require.NoError(t, err)
		require.Equal(t, "complete", job.State)
		require.NotNil(t, job.Object)
		info, err := store.client.StatObject(ctx, store.bucket, job.Object.Key, minio.StatObjectOptions{VersionID: job.Object.VersionID})
		require.NoError(t, err)
		if segment < 2 {
			assert.Regexp(t, `-[2-9][0-9]*$`, info.ETag, "full segments must use real multipart storage")
		}
		object, err := store.client.GetObject(ctx, store.bucket, job.Object.Key, minio.GetObjectOptions{VersionID: job.Object.VersionID})
		require.NoError(t, err)
		decoder := json.NewDecoder(object)
		var manifest exportManifest
		require.NoError(t, decoder.Decode(&manifest))
		assert.Equal(t, "manifest", manifest.Type)
		assert.Equal(t, previousHash, manifest.PreviousHash)
		assert.Equal(t, sequence, manifest.AfterSequence)
		for sequence < job.HeadSequence {
			var record exportEvent
			require.NoError(t, decoder.Decode(&record))
			sequence++
			assert.Equal(t, "event", record.Type)
			assert.Equal(t, sequence, record.Event.Sequence)
			assert.Equal(t, previousHash, record.Event.PreviousHash)
			previousHash = record.Event.EventHash
		}
		assert.Equal(t, previousHash, manifest.HeadHash)
		var completion exportComplete
		require.NoError(t, decoder.Decode(&completion))
		assert.Equal(t, "complete", completion.Type)
		assert.Equal(t, job.HeadSequence-job.AfterSequence, completion.ExportedEvents)
		assert.ErrorIs(t, decoder.Decode(&json.RawMessage{}), io.EOF)
		require.NoError(t, object.Close())
	}
	assert.EqualValues(t, eventCount, sequence)
	_, err = h.service.RequestArchive(ctx, 11)
	assert.ErrorIs(t, err, ErrArchiveEmpty)
	status, err := h.service.archiveStatus(ctx, 11)
	require.NoError(t, err)
	assert.EqualValues(t, 3, status.CompletedJobs)
	assert.EqualValues(t, eventCount, status.ArchivedEvents)
	assert.Zero(t, status.PendingEvents)
	assert.EqualValues(t, eventCount+6, status.TotalEvents, "all source events and job audits remain")
	integrity, err := h.service.Verify(ctx, 11)
	require.NoError(t, err)
	assert.True(t, integrity.Valid)
}
