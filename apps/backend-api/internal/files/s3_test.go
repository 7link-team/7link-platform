package files

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestS3ProtocolMultipartCopyRangeAndReclamation(t *testing.T) {
	var mu sync.Mutex
	var completed, aborted, removed, uploaded, copied int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		assert.Contains(t, r.Header.Get("Authorization"), "AWS4-HMAC-SHA256")
		key := strings.TrimPrefix(r.URL.Path, "/files-test/")
		q := r.URL.Query()
		w.Header().Set("ETag", `"token"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		if r.Method == http.MethodHead {
			if key == "objects/source" || key == "objects/empty" {
				size := "6"
				if key == "objects/empty" {
					size = "0"
				}
				w.Header().Set("Content-Length", size)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == http.MethodGet && q.Has("uploads"):
			_, _ = io.WriteString(w, `<ListMultipartUploadsResult><Bucket>files-test</Bucket><IsTruncated>false</IsTruncated><Upload><Key>objects/pending</Key><UploadId>pending-2</UploadId><Initiated>2006-01-02T15:04:05.000Z</Initiated></Upload><Upload><Key>objects/unrelated</Key><UploadId>unrelated</UploadId><Initiated>2006-01-02T15:04:05.000Z</Initiated></Upload></ListMultipartUploadsResult>`)
		case r.Method == http.MethodPost && q.Has("uploads"):
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>files-test</Bucket><Key>`+key+`</Key><UploadId>upload-1</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
			copied++
			assert.Equal(t, `"token"`, r.Header.Get("X-Amz-Copy-Source-If-Match"))
			assert.Equal(t, "bytes=0-5", r.Header.Get("X-Amz-Copy-Source-Range"))
			_, _ = io.WriteString(w, `<CopyPartResult><ETag>"token"</ETag><LastModified>2006-01-02T15:04:05.000Z</LastModified></CopyPartResult>`)
		case r.Method == http.MethodPut:
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Contains(t, string(body), "abc")
			uploaded++
		case r.Method == http.MethodGet && q.Get("uploadId") != "":
			_, _ = io.WriteString(w, `<ListPartsResult><Bucket>files-test</Bucket><Key>`+key+`</Key><UploadId>upload-1</UploadId><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>"token"</ETag><Size>3</Size><LastModified>2006-01-02T15:04:05.000Z</LastModified></Part></ListPartsResult>`)
		case r.Method == http.MethodPost && q.Get("uploadId") != "":
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Contains(t, string(body), "<PartNumber>1</PartNumber>")
			assert.Equal(t, "*", r.Header.Get("If-None-Match"))
			completed++
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><Bucket>files-test</Bucket><Key>`+key+`</Key><ETag>"token"</ETag></CompleteMultipartUploadResult>`)
		case r.Method == http.MethodDelete:
			if q.Get("uploadId") != "" {
				assert.NotEqual(t, "unrelated", q.Get("uploadId"))
				aborted++
			} else {
				removed++
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/octet-stream")
			body := "abcdef"
			if r.Header.Get("Range") != "" {
				assert.Equal(t, "bytes=1-3", r.Header.Get("Range"))
				assert.Equal(t, `"token"`, r.Header.Get("If-Match"))
				body = "bcd"
				w.Header().Set("Content-Range", "bytes 1-3/6")
				w.WriteHeader(http.StatusPartialContent)
			}
			_, _ = io.WriteString(w, body)
		default:
			t.Errorf("unexpected storage request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	s, err := NewS3(DriverConfig{"endpoint": server.URL, "bucket": "files-test", "region": "us-east-1", "access_key": "fixture-access", "secret_key": "fixture-secret"})
	require.NoError(t, err)
	require.Equal(t, Capabilities{RangeRead: true, Multipart: true, ServerCopy: true}, s.Capabilities())
	fixtureDB, _, _, _, _ := migrationFixture(t)
	dispatch := &backendDriver{active: "s3", drivers: map[string]Driver{"s3": s}, prefix: "move_", db: func() *gorm.DB { return fixtureDB }}
	uri, err := dispatch.Put(t.Context(), "objects/single", strings.NewReader("abc"), 3)
	require.NoError(t, err)
	require.Equal(t, "s3://files-test/objects/single", uri)
	handle, err := dispatch.Begin(t.Context(), "objects/new")
	require.NoError(t, err)
	require.Equal(t, "upload-1", handle)
	part, err := dispatch.WritePart(t.Context(), "objects/new", handle, 1, strings.NewReader("abc"), 3)
	require.NoError(t, err)
	require.Equal(t, PartReceipt{Index: 1, Length: 3, Token: "token"}, part)
	parts, err := dispatch.InspectParts(t.Context(), "objects/new", handle)
	require.NoError(t, err)
	require.Equal(t, []PartReceipt{part}, parts)
	uri, err = dispatch.Complete(t.Context(), "objects/new", handle, parts)
	require.NoError(t, err)
	require.Equal(t, "s3://files-test/objects/new", uri)
	bad := part
	bad.Token = "wrong"
	_, err = dispatch.Complete(t.Context(), "objects/new", handle, []PartReceipt{bad})
	require.ErrorIs(t, err, ErrLength)
	_, err = dispatch.Complete(t.Context(), "objects/new", handle, append(parts, part))
	require.ErrorIs(t, err, ErrLength)
	_, err = dispatch.Begin(t.Context(), "objects/source")
	require.ErrorIs(t, err, ErrExists)
	reader, err := s.Open(t.Context(), "objects/source")
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "abcdef", string(data))
	reader, err = s.OpenRange(t.Context(), "objects/source", 1, 3)
	require.NoError(t, err)
	data, err = io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "bcd", string(data))
	reader, err = s.OpenRange(t.Context(), "objects/source", 6, 0)
	require.NoError(t, err)
	data, err = io.ReadAll(reader)
	require.NoError(t, err)
	require.Empty(t, data)
	require.NoError(t, reader.Close())
	_, err = s.OpenRange(t.Context(), "objects/source", 5, 2)
	require.ErrorIs(t, err, ErrRange)
	uri, err = dispatch.Copy(t.Context(), "objects/source", "objects/copy")
	require.NoError(t, err)
	require.Equal(t, "s3://files-test/objects/copy", uri)
	require.NoError(t, dispatch.Abort(t.Context(), "objects/new", handle))
	require.NoError(t, dispatch.ReclaimWrite(t.Context(), "objects/pending"))
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 2, uploaded)
	require.Equal(t, 2, completed)
	require.Equal(t, 1, copied)
	require.Equal(t, 3, aborted)
	require.Equal(t, 1, removed)
}

func TestS3RejectsInvalidKeysHandlesAndLengthBeforeNetwork(t *testing.T) {
	s := &S3{}
	ctx := t.Context()
	_, err := s.Put(ctx, "../bad", nil, 0)
	require.ErrorIs(t, err, ErrInvalidKey)
	_, err = s.Put(ctx, "valid", nil, 0)
	require.ErrorIs(t, err, ErrLength)
	_, err = s.Put(ctx, "valid", strings.NewReader("x"), 0)
	require.ErrorIs(t, err, ErrLength)
	_, err = s.Open(ctx, "../bad")
	require.ErrorIs(t, err, ErrInvalidKey)
	_, err = s.OpenRange(ctx, "../bad", 0, 0)
	require.ErrorIs(t, err, ErrInvalidKey)
	_, err = s.OpenRange(ctx, "valid", -1, 0)
	require.ErrorIs(t, err, ErrRange)
	require.ErrorIs(t, s.Delete(ctx, "../bad"), ErrInvalidKey)
	_, err = s.Begin(ctx, "../bad")
	require.ErrorIs(t, err, ErrInvalidKey)
	for _, key := range []string{"../bad", "valid"} {
		_, err = s.WritePart(ctx, key, "", 0, nil, 0)
		require.Error(t, err)
		_, err = s.InspectParts(ctx, key, "")
		require.Error(t, err)
		_, err = s.Complete(ctx, key, "", nil)
		require.Error(t, err)
		require.Error(t, s.Abort(ctx, key, ""))
	}
	_, err = s.Copy(ctx, "../bad", "valid")
	require.ErrorIs(t, err, ErrInvalidKey)
	_, err = s.Copy(ctx, "valid", "../bad")
	require.ErrorIs(t, err, ErrInvalidKey)
	require.ErrorIs(t, s.ReclaimWrite(ctx, "../bad"), ErrInvalidKey)
	for _, code := range []string{"NoSuchKey", "NoSuchObject", "PreconditionFailed", "ConditionalRequestConflict", "IncompleteBody", "EntityTooSmall", "XAmzContentSHA256Mismatch", "BadDigest"} {
		require.Error(t, s3Error(minio.ErrorResponse{Code: code}))
	}
}

func TestS3ListingPaginationPreservesOffsetsAndRejectsStalledRemoteCursors(t *testing.T) {
	var pages, aborts, deletes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		key := strings.TrimPrefix(r.URL.Path, "/files-test/")
		q := r.URL.Query()
		if q.Has("uploads") {
			if q.Get("prefix") == "stalled-uploads" {
				_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>true</IsTruncated></ListMultipartUploadsResult>`)
				return
			}
			pages++
			if q.Get("key-marker") == "" {
				_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>true</IsTruncated><NextKeyMarker>next</NextKeyMarker><NextUploadIdMarker>2</NextUploadIdMarker></ListMultipartUploadsResult>`)
			} else {
				assert.Equal(t, "next", q.Get("key-marker"))
				assert.Equal(t, "2", q.Get("upload-id-marker"))
				_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated><Upload><Key>pending</Key><UploadId>2</UploadId></Upload></ListMultipartUploadsResult>`)
			}
			return
		}
		if r.Method == http.MethodDelete {
			if q.Get("uploadId") != "" {
				aborts++
			} else {
				deletes++
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if key == "stalled-parts" {
			_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>true</IsTruncated><NextPartNumberMarker>0</NextPartNumberMarker></ListPartsResult>`)
			return
		}
		if q.Get("part-number-marker") == "0" || q.Get("part-number-marker") == "" {
			_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>true</IsTruncated><NextPartNumberMarker>1</NextPartNumberMarker><Part><PartNumber>1</PartNumber><ETag>"first"</ETag><Size>5</Size></Part></ListPartsResult>`)
		} else {
			assert.Equal(t, "1", q.Get("part-number-marker"))
			_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>2</PartNumber><ETag>"last"</ETag><Size>3</Size></Part></ListPartsResult>`)
		}
	}))
	defer server.Close()
	s, err := NewS3(DriverConfig{"endpoint": server.URL, "bucket": "files-test", "region": "us-east-1", "access_key": "fixture-access", "secret_key": "fixture-secret"})
	require.NoError(t, err)
	parts, err := s.InspectParts(t.Context(), "parts", "handle")
	require.NoError(t, err)
	require.Equal(t, []PartReceipt{{Index: 1, Length: 5, Token: "first"}, {Index: 2, Offset: 5, Length: 3, Token: "last"}}, parts)
	_, err = s.InspectParts(t.Context(), "stalled-parts", "handle")
	require.ErrorIs(t, err, ErrUnsupported)
	require.ErrorIs(t, s.ReclaimWrite(t.Context(), "stalled-uploads"), ErrUnsupported)
	require.NoError(t, s.ReclaimWrite(t.Context(), "pending"))
	require.Equal(t, 2, pages)
	require.Equal(t, 1, aborts)
	require.Equal(t, 1, deletes)
}

func TestS3RemoteErrorsArePropagatedWithoutPublishingReceipts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.Method == http.MethodDelete && r.URL.Query().Get("uploadId") == "already-gone" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`)
	}))
	defer server.Close()
	s, err := NewS3(DriverConfig{"endpoint": server.URL, "bucket": "files-test", "region": "us-east-1", "access_key": "fixture-access", "secret_key": "fixture-secret"})
	require.NoError(t, err)
	_, err = s.Begin(t.Context(), "object")
	require.Error(t, err)
	part, err := s.WritePart(t.Context(), "object", "handle", 1, strings.NewReader("x"), 1)
	require.Error(t, err)
	require.Empty(t, part.Token)
	_, err = s.InspectParts(t.Context(), "object", "handle")
	require.Error(t, err)
	_, err = s.Complete(t.Context(), "object", "handle", []PartReceipt{{Index: 1, Length: 1, Token: "token"}})
	require.Error(t, err)
	_, err = s.Copy(t.Context(), "source", "target")
	require.Error(t, err)
	_, err = s.OpenRange(t.Context(), "source", 0, 1)
	require.Error(t, err)
	require.Error(t, s.ReclaimWrite(t.Context(), "object"))
	require.Error(t, s.Abort(t.Context(), "object", "handle"))
	require.NoError(t, s.Abort(t.Context(), "object", "already-gone"))
}

func TestS3CredentialFilesAndEncryptionConfiguration(t *testing.T) {
	access, secret := filepath.Join(t.TempDir(), "access"), filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(access, []byte(" fixture-access\n"), 0600))
	require.NoError(t, os.WriteFile(secret, []byte(" fixture-secret\n"), 0600))
	for _, mode := range []string{"AES256", "aws:kms"} {
		s, err := NewS3(DriverConfig{"endpoint": "https://storage.example", "bucket": "files-test", "access_key_file": access, "secret_key_file": secret, "sse": mode, "sse_kms_key_id": "fixture-key", "prefix": "deployment"})
		require.NoError(t, err)
		require.NotNil(t, s.sse)
		key, err := s.key("objects/1")
		require.NoError(t, err)
		require.Equal(t, "deployment/objects/1", key)
	}
	for _, config := range []DriverConfig{
		{"endpoint": "https://storage.example", "bucket": "files-test"},
		{"endpoint": "https://storage.example", "bucket": "files-test", "access_key": "inline", "access_key_file": access, "secret_key_file": secret},
		{"endpoint": "https://storage.example", "bucket": "files-test", "access_key_file": access + "-missing", "secret_key_file": secret},
		{"endpoint": "https://storage.example", "bucket": "files-test", "access_key_file": access, "secret_key_file": secret, "sse": "aws:kms"},
	} {
		s, err := NewS3(config)
		require.Error(t, err)
		require.Nil(t, s)
	}
}

func TestS3ConfigurationAndExactLengths(t *testing.T) {
	for _, config := range []DriverConfig{{}, {"endpoint": "ftp://localhost", "bucket": "files-test"}, {"endpoint": "http://localhost/path", "bucket": "files-test"}, {"endpoint": "http://user:secret@localhost", "bucket": "files-test"}, {"endpoint": "http://localhost", "bucket": "invalid bucket"}, {"endpoint": "http://localhost", "bucket": "files-test", "access_key": "test", "secret_key": "test", "prefix": "../bad"}, {"endpoint": "http://localhost", "bucket": "files-test", "access_key": "test", "secret_key": "test", "sse": "invalid"}} {
		_, err := NewS3(config)
		require.Error(t, err)
	}
	for _, test := range []struct {
		body  string
		size  int64
		valid bool
	}{{"a", 1, true}, {"abc", 3, true}, {"ab", 3, false}, {"abcd", 3, false}, {"", 1, false}} {
		reader := &exactReader{source: strings.NewReader(test.body), remaining: test.size}
		body, err := io.ReadAll(reader)
		if test.valid {
			assert.NoError(t, err)
			require.Equal(t, test.body, string(body))
		} else {
			require.ErrorIs(t, err, ErrLength)
		}
	}
}

func TestS3RejectsUnconfigurableDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = nil
	t.Cleanup(func() { http.DefaultTransport = original })
	driver, err := NewS3(DriverConfig{"endpoint": "https://storage.invalid", "bucket": "files-test", "access_key": "test-access", "secret_key": "test-secret"})
	require.Nil(t, driver)
	require.ErrorContains(t, err, "default HTTP transport is not configurable")
}

func TestS3ConditionalHeadersAndFailureMapping(t *testing.T) {
	var method, condition, checksum string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, condition, checksum = r.Method, r.Header.Get("If-None-Match"), r.Header.Get("X-Amz-Trailer")
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code></Error>`)
	}))
	defer server.Close()
	driver, err := NewS3(DriverConfig{"endpoint": server.URL, "bucket": "files-test", "region": "us-east-1", "access_key": "test-access", "secret_key": "test-secret"})
	require.NoError(t, err)
	_, err = driver.Put(t.Context(), "object/1", strings.NewReader("abc"), 3)
	require.ErrorIs(t, err, ErrExists)
	require.Equal(t, "PUT", method)
	require.Equal(t, "*", condition)
	require.Contains(t, strings.ToLower(checksum), "sha256")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = driver.Put(ctx, "object/2", strings.NewReader("abc"), 3)
	require.ErrorIs(t, err, context.Canceled)
	_, err = driver.Open(t.Context(), "../invalid")
	require.ErrorIs(t, err, ErrInvalidKey)
	_, err = driver.OpenRange(t.Context(), "object/1", -1, 1)
	require.ErrorIs(t, err, ErrRange)
}

// An actual S3-compatible endpoint is required for this acceptance test. The
// supplied credentials must refer to an isolated disposable test deployment.
func realS3(t *testing.T) *S3 {
	t.Helper()
	endpoint := os.Getenv("FILES_S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set FILES_S3_TEST_ENDPOINT for real S3 acceptance")
	}
	driver, err := NewS3(DriverConfig{"endpoint": endpoint, "bucket": "files-fixture-acceptance", "region": "us-east-1", "access_key": os.Getenv("FILES_S3_TEST_ACCESS_KEY"), "secret_key": os.Getenv("FILES_S3_TEST_SECRET_KEY")})
	require.NoError(t, err)
	err = driver.core.MakeBucket(t.Context(), driver.bucket, minio.MakeBucketOptions{Region: "us-east-1"})
	if code := minio.ToErrorResponse(err).Code; code != "BucketAlreadyOwnedByYou" && code != "BucketAlreadyExists" {
		require.NoError(t, err)
	}
	return driver
}

func TestS3RealNativeStorage(t *testing.T) {
	s := realS3(t)
	key := "native/" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	require.NoError(t, s.ReclaimWrite(t.Context(), key))
	t.Cleanup(func() { require.NoError(t, s.ReclaimWrite(context.Background(), key)) })
	body := bytes.Repeat([]byte("streamed data\n"), 10000)
	uri, err := s.Put(t.Context(), key, bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(uri, "s3://"))
	_, err = s.Put(t.Context(), key, strings.NewReader("changed"), 7)
	require.ErrorIs(t, err, ErrExists)
	reader, err := s.Open(t.Context(), key)
	require.NoError(t, err)
	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, body, actual)
	reader, err = s.OpenRange(t.Context(), key, 3, 19)
	require.NoError(t, err)
	actual, err = io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, body[3:22], actual)
	for _, bounds := range [][2]int64{{int64(len(body)), 1}, {int64(len(body)) - 2, 3}, {int64(len(body)) + 1, 0}} {
		_, err = s.OpenRange(t.Context(), key, bounds[0], bounds[1])
		require.ErrorIs(t, err, ErrRange)
	}
	reader, err = s.OpenRange(t.Context(), key, int64(len(body)), 0)
	require.NoError(t, err)
	actual, err = io.ReadAll(reader)
	require.NoError(t, err)
	require.Empty(t, actual)
	require.NoError(t, reader.Close())
	_, err = s.OpenRange(t.Context(), key+"-missing", 0, 0)
	require.ErrorIs(t, err, ErrNotFound)
	copyKey := key + "-copy"
	require.NoError(t, s.Delete(t.Context(), copyKey))
	t.Cleanup(func() { require.NoError(t, s.Delete(context.Background(), copyKey)) })
	_, err = s.Copy(t.Context(), key, copyKey)
	require.NoError(t, err)
	_, err = s.Copy(t.Context(), key, copyKey)
	require.ErrorIs(t, err, ErrExists, "server copy must preserve immutable destinations")
	for _, test := range []struct {
		body string
		size int64
	}{{"abc", 4}, {"abcd", 3}, {"x", 0}} {
		bad := key + "-bad"
		require.NoError(t, s.Delete(t.Context(), bad))
		_, err = s.Put(t.Context(), bad, strings.NewReader(test.body), test.size)
		require.Error(t, err)
		_, err = s.Open(t.Context(), bad)
		require.ErrorIs(t, err, ErrNotFound, "failed length checks must not publish bytes")
	}
	// Conditional writes race at the server, not at a process-local lock.
	raceKey := key + "-race"
	require.NoError(t, s.Delete(t.Context(), raceKey))
	t.Cleanup(func() { require.NoError(t, s.Delete(context.Background(), raceKey)) })
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Put(t.Context(), raceKey, bytes.NewReader(body), int64(len(body)))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else {
			require.ErrorIs(t, err, ErrExists)
			conflicts++
		}
	}
	require.Equal(t, 1, wins)
	require.Equal(t, 1, conflicts)
}

func TestS3RealMultipartAndReclaim(t *testing.T) {
	s := realS3(t)
	key := "native/multipart"
	require.NoError(t, s.ReclaimWrite(t.Context(), key))
	t.Cleanup(func() { require.NoError(t, s.ReclaimWrite(context.Background(), key)) })
	handle, err := s.Begin(t.Context(), key)
	require.NoError(t, err)
	partBody := bytes.Repeat([]byte("p"), 5<<20)
	first, err := s.WritePart(t.Context(), key, handle, 1, bytes.NewReader(partBody), int64(len(partBody)))
	require.NoError(t, err)
	last, err := s.WritePart(t.Context(), key, handle, 2, strings.NewReader("tail"), 4)
	require.NoError(t, err)
	parts, err := s.InspectParts(t.Context(), key, handle)
	require.NoError(t, err)
	require.Len(t, parts, 2)
	require.Equal(t, int64(len(partBody)), parts[1].Offset)
	bad := last
	bad.Token = "invalid"
	_, err = s.Complete(t.Context(), key, handle, []PartReceipt{first, bad})
	require.ErrorIs(t, err, ErrLength)
	_, err = s.Complete(t.Context(), key, handle, []PartReceipt{first, last})
	require.NoError(t, err)
	reader, err := s.Open(t.Context(), key)
	require.NoError(t, err)
	sums := NewChecksums()
	_, err = io.Copy(sums, reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	want := NewChecksums()
	_, _ = want.Write(partBody)
	_, _ = want.Write([]byte("tail"))
	require.Equal(t, want.Digests(), sums.Digests())
	_, err = s.Begin(t.Context(), key)
	require.ErrorIs(t, err, ErrExists)
	other := key + "-abandoned"
	handle, err = s.Begin(t.Context(), other)
	require.NoError(t, err)
	_, err = s.WritePart(t.Context(), other, handle, 1, strings.NewReader("data"), 4)
	require.NoError(t, err)
	require.NoError(t, s.ReclaimWrite(t.Context(), other))
	require.NoError(t, s.Abort(t.Context(), other, handle))
	require.NoError(t, s.Abort(t.Context(), other, handle))
	_, err = s.InspectParts(t.Context(), other, handle)
	require.Error(t, err)
}
