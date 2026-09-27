package audit

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	auditMinIOTestUser     = "sevenlink-minio"        //nolint:gosec // local integration-test credential
	auditMinIOTestPassword = "sevenlink-minio-secret" //nolint:gosec // local integration-test credential
)

type fakeS3 struct {
	mu           sync.Mutex
	bucket       bool
	objectLock   bool
	fail         bool
	failMethod   string
	failContains string
	objects      map[string][]byte
	retention    map[string]string
	lockModes    map[string]string
	versions     map[string]string
	requestPaths []string
}

func newFakeS3() *fakeS3 {
	return &fakeS3{
		objects: make(map[string][]byte), retention: make(map[string]string),
		lockModes: make(map[string]string), versions: make(map[string]string),
	}
}

func (store *fakeS3) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.requestPaths = append(store.requestPaths, request.Method+" "+request.URL.RequestURI())
	if store.fail || (store.failMethod == request.Method && strings.Contains(request.URL.RequestURI(), store.failContains)) {
		fakeS3Error(writer, http.StatusForbidden, "AccessDenied", "fixture unavailable")
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		fakeS3Error(writer, http.StatusNotFound, "NoSuchBucket", "missing bucket")
		return
	}
	if request.URL.Query().Has("location") {
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(writer, `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></LocationConstraint>`)
		return
	}
	if len(parts) == 1 {
		store.handleBucket(writer, request)
		return
	}
	key, err := url.PathUnescape(strings.Join(parts[1:], "/"))
	if err != nil {
		fakeS3Error(writer, http.StatusBadRequest, "InvalidURI", err.Error())
		return
	}
	store.handleObject(writer, request, key)
}

func (store *fakeS3) handleBucket(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodHead:
		if !store.bucket {
			fakeS3Error(writer, http.StatusNotFound, "NoSuchBucket", "bucket does not exist")
			return
		}
		writer.WriteHeader(http.StatusOK)
	case http.MethodPut:
		store.bucket = true
		store.objectLock = strings.EqualFold(request.Header.Get("X-Amz-Bucket-Object-Lock-Enabled"), "true")
		writer.WriteHeader(http.StatusOK)
	case http.MethodGet:
		if !store.bucket {
			fakeS3Error(writer, http.StatusNotFound, "NoSuchBucket", "bucket does not exist")
			return
		}
		if request.URL.Query().Has("object-lock") {
			lock := "Disabled"
			if store.objectLock {
				lock = "Enabled"
			}
			writer.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprintf(writer, `<ObjectLockConfiguration><ObjectLockEnabled>%s</ObjectLockEnabled></ObjectLockConfiguration>`, lock)
			return
		}
		store.writeList(writer, request)
	default:
		fakeS3Error(writer, http.StatusMethodNotAllowed, "MethodNotAllowed", "unsupported method")
	}
}

func (store *fakeS3) handleObject(writer http.ResponseWriter, request *http.Request, key string) {
	if !store.bucket {
		fakeS3Error(writer, http.StatusNotFound, "NoSuchBucket", "bucket does not exist")
		return
	}
	if version := request.URL.Query().Get("versionId"); version != "" && version != store.versions[key] {
		fakeS3Error(writer, http.StatusNotFound, "NoSuchVersion", "object version does not exist")
		return
	}
	if request.Method == http.MethodGet && request.URL.Query().Has("retention") {
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(writer, `<Retention xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Mode>%s</Mode><RetainUntilDate>%s</RetainUntilDate></Retention>`, store.lockModes[key], store.retention[key])
		return
	}
	switch request.Method {
	case http.MethodHead:
		payload, exists := store.objects[key]
		if !exists {
			fakeS3Error(writer, http.StatusNotFound, "NoSuchKey", "object does not exist")
			return
		}
		writer.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		writer.Header().Set("Last-Modified", auditTestNow.Format(http.TimeFormat))
		writer.Header().Set("ETag", `"fixture"`)
		writer.Header().Set("X-Amz-Version-Id", store.versions[key])
		writer.WriteHeader(http.StatusOK)
	case http.MethodPut:
		if _, exists := store.objects[key]; exists && request.Header.Get("If-None-Match") == "*" {
			fakeS3Error(writer, http.StatusPreconditionFailed, "PreconditionFailed", "object already exists")
			return
		}
		if request.Header.Get("X-Amz-Object-Lock-Mode") != "" && !store.objectLock {
			fakeS3Error(writer, http.StatusBadRequest, "InvalidRequest", "object lock is disabled")
			return
		}
		payload, err := readFakeS3Body(request)
		if err != nil {
			fakeS3Error(writer, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		store.objects[key] = payload
		store.retention[key] = request.Header.Get("X-Amz-Object-Lock-Retain-Until-Date")
		store.lockModes[key] = request.Header.Get("X-Amz-Object-Lock-Mode")
		store.versions[key] = fmt.Sprintf("version-%d", len(store.versions)+1)
		writer.Header().Set("ETag", `"fixture"`)
		writer.Header().Set("X-Amz-Version-Id", store.versions[key])
		writer.WriteHeader(http.StatusOK)
	case http.MethodGet:
		payload, exists := store.objects[key]
		if !exists {
			fakeS3Error(writer, http.StatusNotFound, "NoSuchKey", "object does not exist")
			return
		}
		writer.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		writer.Header().Set("Last-Modified", auditTestNow.Format(http.TimeFormat))
		writer.Header().Set("ETag", `"fixture"`)
		writer.Header().Set("X-Amz-Version-Id", store.versions[key])
		_, _ = writer.Write(payload)
	case http.MethodDelete:
		delete(store.objects, key)
		delete(store.retention, key)
		writer.WriteHeader(http.StatusNoContent)
	default:
		fakeS3Error(writer, http.StatusMethodNotAllowed, "MethodNotAllowed", "unsupported method")
	}
}

func readFakeS3Body(request *http.Request) ([]byte, error) {
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	firstLineEnd := bytes.IndexByte(raw, '\n')
	if firstLineEnd < 0 || !bytes.Contains(raw[:firstLineEnd], []byte(";chunk-signature=")) {
		return raw, nil
	}
	reader := bufio.NewReader(bytes.NewReader(raw))
	var payload bytes.Buffer
	for {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		sizeText := strings.SplitN(strings.TrimSpace(header), ";", 2)[0]
		size, err := strconv.ParseInt(sizeText, 16, 64)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			return payload.Bytes(), nil
		}
		if _, err := io.CopyN(&payload, reader, size); err != nil {
			return nil, err
		}
		if _, err := reader.ReadString('\n'); err != nil {
			return nil, err
		}
	}
}

func (store *fakeS3) writeList(writer http.ResponseWriter, request *http.Request) {
	prefix := request.URL.Query().Get("prefix")
	type content struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int    `xml:"Size"`
		StorageClass string `xml:"StorageClass"`
	}
	type version struct {
		Key          string `xml:"Key"`
		VersionID    string `xml:"VersionId"`
		IsLatest     bool   `xml:"IsLatest"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int    `xml:"Size"`
		StorageClass string `xml:"StorageClass"`
	}
	type versionsResult struct {
		XMLName        xml.Name  `xml:"ListVersionsResult"`
		Xmlns          string    `xml:"xmlns,attr"`
		Name           string    `xml:"Name"`
		Prefix         string    `xml:"Prefix"`
		MaxKeys        int       `xml:"MaxKeys"`
		IsTruncated    bool      `xml:"IsTruncated"`
		ObjectVersions []version `xml:"Version"`
	}
	type result struct {
		XMLName     xml.Name  `xml:"ListBucketResult"`
		Xmlns       string    `xml:"xmlns,attr"`
		Name        string    `xml:"Name"`
		Prefix      string    `xml:"Prefix"`
		KeyCount    int       `xml:"KeyCount"`
		MaxKeys     int       `xml:"MaxKeys"`
		IsTruncated bool      `xml:"IsTruncated"`
		Contents    []content `xml:"Contents"`
	}
	keys := make([]string, 0, len(store.objects))
	for key := range store.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if request.URL.Query().Has("versions") {
		response := versionsResult{
			Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", Name: "anchors", Prefix: prefix,
			MaxKeys: 1000,
		}
		for _, key := range keys {
			response.ObjectVersions = append(response.ObjectVersions, version{
				Key: key, VersionID: store.versions[key], IsLatest: true,
				LastModified: auditTestNow.Format(time.RFC3339), ETag: `"fixture"`,
				Size: len(store.objects[key]), StorageClass: "STANDARD",
			})
		}
		payload, _ := xml.Marshal(response)
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write(payload)
		return
	}
	response := result{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", Name: "anchors", Prefix: prefix,
		KeyCount: len(keys), MaxKeys: 1000,
	}
	for _, key := range keys {
		response.Contents = append(response.Contents, content{
			Key: key, LastModified: auditTestNow.Format(time.RFC3339), ETag: `"fixture"`,
			Size: len(store.objects[key]), StorageClass: "STANDARD",
		})
	}
	payload, _ := xml.Marshal(response)
	writer.Header().Set("Content-Type", "application/xml")
	_, _ = writer.Write(payload)
}

func fakeS3Error(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/xml")
	writer.WriteHeader(status)
	_, _ = fmt.Fprintf(writer, `<Error><Code>%s</Code><Message>%s</Message></Error>`, code, message)
}

func fakeAnchorStore(t *testing.T, fixture *fakeS3, bucket string) (*AnchorStore, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	store, err := NewAnchorStore(AnchorConfig{
		Enabled: true, Endpoint: server.URL, Bucket: bucket,
		AccessKey: "access", SecretKey: "secret", RetentionDays: 30,
	})
	require.NoError(t, err)
	return store, server
}

func auditTestSigner(t *testing.T) *RootSigner {
	t.Helper()
	seed := make([]byte, 32)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	signer, err := NewRootSigner(base64.StdEncoding.EncodeToString(seed))
	require.NoError(t, err)
	return signer
}

func TestAuditAnchorAndSignedRootLifecycle(t *testing.T) {
	h := newAuditHarness(t, "sl_")
	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "audit-roots")
	service := NewServiceWithAnchorAndSigner(h.runtime, store, auditTestSigner(t), newAuditTestIDs(2000))
	service.now = h.service.now
	tenantA, tenantB := guid.ID(1001), guid.ID(1002)
	for range 2 {
		_, err := service.Append(t.Context(), EventInput{TenantID: tenantA, EventType: "created", Outcome: "success"})
		require.NoError(t, err)
	}
	_, err := service.Append(t.Context(), EventInput{TenantID: tenantB, EventType: "foreign", Outcome: "success"})
	require.NoError(t, err)

	first, err := service.SignRoot(t.Context(), tenantA)
	require.NoError(t, err)
	assert.Equal(t, int64(2), first.HeadSequence)
	assert.True(t, VerifyRootSignature(first))
	again, err := service.SignRoot(t.Context(), tenantA)
	require.NoError(t, err)
	assert.Equal(t, first, again)

	_, err = service.Append(t.Context(), EventInput{TenantID: tenantA, EventType: "updated", Outcome: "success"})
	require.NoError(t, err)
	second, err := service.Anchor(t.Context(), tenantA)
	require.NoError(t, err)
	assert.Equal(t, int64(3), second.HeadSequence)
	assert.Equal(t, first.AnchorHash, second.PreviousAnchorHash)
	assert.True(t, VerifyRootSignature(second))

	anchors, err := store.List(t.Context(), tenantA)
	require.NoError(t, err)
	require.Len(t, anchors, 2)
	assert.Equal(t, []int64{2, 3}, []int64{anchors[0].HeadSequence, anchors[1].HeadSequence})
	foreign, err := store.List(t.Context(), tenantB)
	require.NoError(t, err)
	assert.Empty(t, foreign)
	integrity, err := service.Verify(t.Context(), tenantA)
	require.NoError(t, err)
	assert.True(t, integrity.Valid)
	assert.True(t, integrity.Anchor.Enabled)
	assert.True(t, integrity.Anchor.SigningEnabled)
	assert.True(t, integrity.Anchor.Valid)
	assert.True(t, integrity.Anchor.Signed)
	assert.True(t, integrity.Anchor.SignatureValid)
	assert.Equal(t, int64(3), integrity.Anchor.Sequence)

	report, err := service.Governance(t.Context(), tenantA)
	require.NoError(t, err)
	assert.Equal(t, int64(3), report.Anchored)
	require.Len(t, report.Anchors, 2)

	fixture.mu.Lock()
	for key, retainUntil := range fixture.retention {
		if strings.HasSuffix(key, ".json") {
			assert.NotEmpty(t, retainUntil)
		}
	}
	fixture.mu.Unlock()
}

func TestAuditAnchorDisabledEmptyUnavailableAndObjectLockFailures(t *testing.T) {
	h := newAuditHarness(t, "")
	_, err := h.service.Anchor(t.Context(), 1)
	assert.ErrorIs(t, err, ErrAnchorDisabled)
	_, err = h.service.Anchor(t.Context(), 0)
	assert.ErrorIs(t, err, ErrAnchorDisabled)

	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "audit-empty")
	service := NewServiceWithAnchor(h.runtime, store, newAuditTestIDs(3000))
	service.now = h.service.now
	_, err = service.Anchor(t.Context(), 0)
	assert.ErrorIs(t, err, ErrInvalidFilter)
	_, err = service.Anchor(t.Context(), 1)
	assert.ErrorIs(t, err, ErrAnchorEmpty)

	fixture.fail = true
	_, err = service.Append(t.Context(), EventInput{TenantID: 1, EventType: "created", Outcome: "success"})
	require.NoError(t, err)
	_, err = service.Anchor(t.Context(), 1)
	assert.ErrorIs(t, err, ErrAnchorUnavailable)
	_, err = service.Verify(t.Context(), 1)
	assert.ErrorIs(t, err, ErrAnchorUnavailable)

	unlocked := newFakeS3()
	unlocked.bucket = true
	unlockedStore, _ := fakeAnchorStore(t, unlocked, "audit-unlocked")
	unlockedService := NewServiceWithAnchor(h.runtime, unlockedStore, newAuditTestIDs(4000))
	unlockedService.now = h.service.now
	_, err = unlockedService.Anchor(t.Context(), 1)
	assert.ErrorIs(t, err, ErrAnchorUnavailable)
}

func TestAnchorStorePutConflictsAndListFiltering(t *testing.T) {
	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "audit-filter")
	anchor := Anchor{
		Schema: anchorSchema, TenantID: 2001, HeadSequence: 5,
		HeadHash: strings.Repeat("c", 64), AnchoredAt: auditTestNow,
	}
	anchor.AnchorHash = computeAnchorHash(anchor)
	require.NoError(t, store.Put(t.Context(), anchor))
	assert.ErrorIs(t, store.Put(t.Context(), anchor), ErrAnchorAlreadyExists)

	fixture.mu.Lock()
	fixture.objects[anchorPrefix+"/2001/readme.txt"] = []byte("ignore")
	fixture.objects[anchorPrefix+"/2001/bad.json"] = []byte(`{"schema":"bad"}`)
	fixture.objects[anchorPrefix+"/2001/6.json"] = []byte(`{"schema":"bad","head_sequence":6}`)
	fixture.mu.Unlock()
	anchors, err := store.List(t.Context(), 2001)
	require.NoError(t, err)
	require.Len(t, anchors, 1)
	assert.Equal(t, anchor, anchors[0])

	fixture.mu.Lock()
	fixture.objects[anchorPrefix+"/2001/7.json"] = []byte("not-json")
	fixture.mu.Unlock()
	_, err = store.List(t.Context(), 2001)
	assert.ErrorContains(t, err, "decode anchor object")
}

func TestAuditAnchorVerificationRejectsCorruptionAndTenantSubstitution(t *testing.T) {
	for name, mutate := range map[string]func(*Anchor){
		"hash": func(anchor *Anchor) { anchor.AnchorHash = strings.Repeat("0", 64) },
		"previous": func(anchor *Anchor) {
			anchor.PreviousAnchorHash = "unexpected"
			anchor.AnchorHash = computeAnchorHash(*anchor)
		},
		"tenant": func(anchor *Anchor) {
			anchor.TenantID++
			anchor.AnchorHash = computeAnchorHash(*anchor)
		},
		"signature": func(anchor *Anchor) { anchor.RootSignature = "invalid" },
	} {
		t.Run(name, func(t *testing.T) {
			h := newAuditHarness(t, "")
			fixture := newFakeS3()
			store, _ := fakeAnchorStore(t, fixture, "audit-corrupt")
			service := NewServiceWithAnchorAndSigner(h.runtime, store, auditTestSigner(t), newAuditTestIDs(5000))
			service.now = h.service.now
			_, err := service.Append(t.Context(), EventInput{TenantID: 3001, EventType: "created", Outcome: "success"})
			require.NoError(t, err)
			anchor, err := service.SignRoot(t.Context(), 3001)
			require.NoError(t, err)
			mutate(&anchor)
			payload, err := json.Marshal(anchor)
			require.NoError(t, err)
			fixture.mu.Lock()
			fixture.objects[store.objectKey(3001, 1)] = payload
			fixture.mu.Unlock()

			integrity, err := service.Verify(t.Context(), 3001)
			require.NoError(t, err)
			assert.False(t, integrity.Anchor.Valid)
		})
	}
}

func TestAuditAnchorRejectsBrokenLocalChainsFutureAnchorsAndUnsignedRoots(t *testing.T) {
	t.Run("repository unavailable", func(t *testing.T) {
		fixture := newFakeS3()
		store, _ := fakeAnchorStore(t, fixture, "audit-unavailable-repo")
		service := &Service{anchor: store, now: func() time.Time { return auditTestNow }}
		_, err := service.Anchor(t.Context(), 1)
		assert.ErrorIs(t, err, errRepositoryUnavailable)
	})

	t.Run("head read failure", func(t *testing.T) {
		h := newAuditHarness(t, "")
		fixture := newFakeS3()
		store, _ := fakeAnchorStore(t, fixture, "audit-head-failure")
		service := NewServiceWithAnchor(h.runtime, store, newAuditTestIDs(6000))
		service.now = h.service.now
		require.NoError(t, h.db.Exec("DROP TABLE iam_audit_heads").Error)
		_, err := service.Anchor(t.Context(), 1)
		assert.ErrorContains(t, err, "read audit head")
	})

	t.Run("tampered local event", func(t *testing.T) {
		h := newAuditHarness(t, "")
		fixture := newFakeS3()
		store, _ := fakeAnchorStore(t, fixture, "audit-local-tamper")
		service := NewServiceWithAnchor(h.runtime, store, newAuditTestIDs(7000))
		service.now = h.service.now
		event, err := service.Append(t.Context(), EventInput{TenantID: 1, EventType: "created", Outcome: "success"})
		require.NoError(t, err)
		require.NoError(t, h.db.Exec("DROP TRIGGER trg_iam_audit_events_no_update").Error)
		require.NoError(t, h.db.Exec("UPDATE iam_audit_events SET event_hash = 'bad' WHERE id = ?", event.ID).Error)
		_, err = service.Anchor(t.Context(), 1)
		assert.ErrorIs(t, err, ErrIntegrity)
	})

	t.Run("future anchor", func(t *testing.T) {
		h := newAuditHarness(t, "")
		fixture := newFakeS3()
		fixture.bucket, fixture.objectLock = true, true
		store, _ := fakeAnchorStore(t, fixture, "audit-future")
		service := NewServiceWithAnchor(h.runtime, store, newAuditTestIDs(8000))
		service.now = h.service.now
		_, err := service.Append(t.Context(), EventInput{TenantID: 1, EventType: "created", Outcome: "success"})
		require.NoError(t, err)
		future := Anchor{
			Schema: anchorSchema, TenantID: 1, HeadSequence: 2,
			HeadHash: strings.Repeat("d", 64), AnchoredAt: auditTestNow,
		}
		future.AnchorHash = computeAnchorHash(future)
		payload, err := json.Marshal(future)
		require.NoError(t, err)
		fixture.objects[store.objectKey(1, 2)] = payload
		_, err = service.Anchor(t.Context(), 1)
		assert.ErrorIs(t, err, ErrIntegrity)
	})

	t.Run("unsigned root", func(t *testing.T) {
		h := newAuditHarness(t, "")
		fixture := newFakeS3()
		store, _ := fakeAnchorStore(t, fixture, "audit-unsigned")
		service := NewServiceWithAnchor(h.runtime, store, newAuditTestIDs(9000))
		service.now = h.service.now
		_, err := service.Append(t.Context(), EventInput{TenantID: 1, EventType: "created", Outcome: "success"})
		require.NoError(t, err)
		_, err = service.SignRoot(t.Context(), 1)
		assert.ErrorIs(t, err, ErrRootSigningDisabled)
		assert.Empty(t, fixture.objects, "a refused signature must not write any external object")
		_, err = service.Anchor(t.Context(), 1)
		require.NoError(t, err)
		before, err := store.List(t.Context(), 1)
		require.NoError(t, err)
		_, err = service.SignRoot(t.Context(), 1)
		assert.ErrorIs(t, err, ErrRootSigningDisabled)
		service.signer = auditTestSigner(t)
		_, err = service.SignRoot(t.Context(), 1)
		assert.ErrorIs(t, err, ErrRootSigningDisabled, "an immutable unsigned commitment cannot be replaced")
		after, err := store.List(t.Context(), 1)
		require.NoError(t, err)
		assert.Equal(t, before, after)
	})

	t.Run("enabled without anchors", func(t *testing.T) {
		h := newAuditHarness(t, "")
		fixture := newFakeS3()
		store, _ := fakeAnchorStore(t, fixture, "audit-no-anchors")
		service := NewServiceWithAnchor(h.runtime, store, newAuditTestIDs(10000))
		service.now = h.service.now
		_, err := service.Append(t.Context(), EventInput{TenantID: 1, EventType: "created", Outcome: "success"})
		require.NoError(t, err)
		integrity, err := service.Verify(t.Context(), 1)
		require.NoError(t, err)
		assert.True(t, integrity.Anchor.Enabled)
		assert.False(t, integrity.Anchor.SigningEnabled)
		assert.True(t, integrity.Anchor.Valid)
		assert.Zero(t, integrity.Anchor.Sequence)
	})
}

func TestAnchorStorePropagatesBucketObjectAndReadFailures(t *testing.T) {
	base := Anchor{
		Schema: anchorSchema, TenantID: 4001, HeadSequence: 1,
		HeadHash: strings.Repeat("e", 64), AnchoredAt: auditTestNow,
	}
	base.AnchorHash = computeAnchorHash(base)

	t.Run("create bucket", func(t *testing.T) {
		fixture := newFakeS3()
		fixture.failMethod, fixture.failContains = http.MethodPut, "/audit-create/"
		store, _ := fakeAnchorStore(t, fixture, "audit-create")
		assert.Error(t, store.Put(t.Context(), base))
	})

	t.Run("stat object", func(t *testing.T) {
		fixture := newFakeS3()
		store, _ := fakeAnchorStore(t, fixture, "audit-stat")
		require.NoError(t, store.ensureBucket(t.Context()))
		fixture.failMethod, fixture.failContains = http.MethodHead, ".json"
		assert.ErrorContains(t, store.Put(t.Context(), base), "stat anchor object")
	})

	t.Run("put object", func(t *testing.T) {
		fixture := newFakeS3()
		store, _ := fakeAnchorStore(t, fixture, "audit-put")
		require.NoError(t, store.ensureBucket(t.Context()))
		fixture.failMethod, fixture.failContains = http.MethodPut, ".json"
		assert.ErrorContains(t, store.Put(t.Context(), base), "put anchor object")
	})

	t.Run("list objects", func(t *testing.T) {
		fixture := newFakeS3()
		store, _ := fakeAnchorStore(t, fixture, "audit-list")
		require.NoError(t, store.ensureBucket(t.Context()))
		fixture.failMethod, fixture.failContains = http.MethodGet, "list-type=2"
		_, err := store.List(t.Context(), base.TenantID)
		assert.ErrorContains(t, err, "list anchor objects")
	})

	t.Run("read object", func(t *testing.T) {
		fixture := newFakeS3()
		store, _ := fakeAnchorStore(t, fixture, "audit-read")
		require.NoError(t, store.Put(t.Context(), base))
		fixture.failMethod, fixture.failContains = http.MethodGet, ".json"
		_, err := store.List(t.Context(), base.TenantID)
		assert.ErrorContains(t, err, "read anchor object")
	})
}

func TestAnchorStoreRealObjectLock(t *testing.T) {
	endpoint := os.Getenv("SEVENLINK_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("real MinIO endpoint not configured; set SEVENLINK_MINIO_ENDPOINT")
	}
	address, secure, err := endpointAddress(endpoint)
	require.NoError(t, err)
	client, err := minio.New(address, &minio.Options{
		Creds:  credentials.NewStaticV4(auditMinIOTestUser, auditMinIOTestPassword, ""),
		Secure: secure,
	})
	require.NoError(t, err)
	store, err := NewAnchorStore(AnchorConfig{
		Enabled: true, Endpoint: endpoint, Bucket: "audit-real-object-lock",
		AccessKey: auditMinIOTestUser, SecretKey: auditMinIOTestPassword, RetentionDays: 30,
	})
	require.NoError(t, err)
	anchor := Anchor{
		Schema: anchorSchema, TenantID: 5001, HeadSequence: 1,
		HeadHash: strings.Repeat("f", 64), AnchoredAt: time.Now().UTC(),
	}
	anchor.AnchorHash = computeAnchorHash(anchor)
	require.NoError(t, store.Put(t.Context(), anchor))

	key := store.objectKey(anchor.TenantID, anchor.HeadSequence)
	info, err := client.StatObject(t.Context(), store.bucket, key, minio.StatObjectOptions{})
	require.NoError(t, err)
	assert.Positive(t, info.Size)
	mode, until, err := client.GetObjectRetention(t.Context(), store.bucket, key, "")
	require.NoError(t, err)
	require.NotNil(t, mode)
	require.NotNil(t, until)
	assert.Equal(t, minio.Compliance, *mode)
	assert.True(t, until.After(time.Now().Add(29*24*time.Hour)))

	anchors, err := store.List(t.Context(), anchor.TenantID)
	require.NoError(t, err)
	require.Len(t, anchors, 1)
	assert.Equal(t, anchor.AnchorHash, anchors[0].AnchorHash)
	assert.ErrorIs(t, store.Put(t.Context(), anchor), ErrAnchorAlreadyExists)

	var versionID string
	for object := range client.ListObjects(t.Context(), store.bucket, minio.ListObjectsOptions{
		Prefix: key, Recursive: true, WithVersions: true,
	}) {
		require.NoError(t, object.Err)
		versionID = object.VersionID
	}
	require.NotEmpty(t, versionID)
	err = client.RemoveObject(t.Context(), store.bucket, key, minio.RemoveObjectOptions{VersionID: versionID})
	assert.Error(t, err, "a compliance-locked anchor version must refuse deletion")
}
