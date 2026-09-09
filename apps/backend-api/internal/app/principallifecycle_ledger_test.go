package app

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	iamaudit "7link.com/platform/internal/iam/audit"
	"github.com/stretchr/testify/require"
)

// principalErasureLedgerFixture is the external system in the composed erasure
// tests. It intentionally speaks S3 over HTTP so those tests cross the same
// module boundary as production instead of replacing the recorder with a stub.
type principalErasureLedgerFixture struct {
	mu        sync.Mutex
	server    *httptest.Server
	objects   map[string][]byte
	retention map[string]string
	versions  map[string]string
}

func newPrincipalErasureLedgerFixture(t *testing.T) *principalErasureLedgerFixture {
	t.Helper()
	fixture := &principalErasureLedgerFixture{
		objects: make(map[string][]byte), retention: make(map[string]string), versions: make(map[string]string),
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *principalErasureLedgerFixture) configure(config *AppConfig) {
	config.IAMAudit.Anchor = IAMAuditAnchor{
		Endpoint: fixture.server.URL, Bucket: "erasure-test", AccessKey: "test-access",
		SecretKey: "test-secret", RetentionDays: 30,
	}
	config.IAMAudit.ErasureLedger = IAMAuditErasureLedger{Enabled: true, Timeout: 5 * time.Second}
}

func (fixture *principalErasureLedgerFixture) obligationCount() int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	count := 0
	for key := range fixture.objects {
		if strings.HasPrefix(key, "audit/erasures/") {
			count++
		}
	}
	return count
}

func (fixture *principalErasureLedgerFixture) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if request.URL.Query().Has("location") {
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(writer, `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></LocationConstraint>`)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) == 1 {
		if request.Method == http.MethodGet && request.URL.Query().Has("versions") {
			fixture.writeVersions(writer, request.URL.Query().Get("prefix"))
			return
		}
		writer.WriteHeader(http.StatusOK)
		return
	}
	key, err := url.PathUnescape(strings.Join(parts[1:], "/"))
	if err != nil {
		principalErasureS3Error(writer, http.StatusBadRequest, "InvalidURI")
		return
	}
	version := request.URL.Query().Get("versionId")
	if version != "" && version != fixture.versions[key] {
		principalErasureS3Error(writer, http.StatusNotFound, "NoSuchVersion")
		return
	}
	if request.Method == http.MethodGet && request.URL.Query().Has("retention") {
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(writer, `<Retention xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Mode>COMPLIANCE</Mode><RetainUntilDate>%s</RetainUntilDate></Retention>`, fixture.retention[key])
		return
	}
	switch request.Method {
	case http.MethodHead:
		payload, exists := fixture.objects[key]
		if !exists {
			principalErasureS3Error(writer, http.StatusNotFound, "NoSuchKey")
			return
		}
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		writer.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		writer.Header().Set("ETag", `"fixture"`)
		writer.Header().Set("X-Amz-Version-Id", fixture.versions[key])
		writer.WriteHeader(http.StatusOK)
	case http.MethodPut:
		if _, exists := fixture.objects[key]; exists && request.Header.Get("If-None-Match") == "*" {
			principalErasureS3Error(writer, http.StatusPreconditionFailed, "PreconditionFailed")
			return
		}
		payload, err := readPrincipalErasureS3Body(request.Body)
		if err != nil {
			principalErasureS3Error(writer, http.StatusBadRequest, "InvalidRequest")
			return
		}
		fixture.objects[key] = payload
		fixture.retention[key] = request.Header.Get("X-Amz-Object-Lock-Retain-Until-Date")
		fixture.versions[key] = "version-" + strconv.Itoa(len(fixture.versions)+1)
		writer.Header().Set("ETag", `"fixture"`)
		writer.Header().Set("X-Amz-Version-Id", fixture.versions[key])
		writer.WriteHeader(http.StatusOK)
	case http.MethodGet:
		payload, exists := fixture.objects[key]
		if !exists {
			principalErasureS3Error(writer, http.StatusNotFound, "NoSuchKey")
			return
		}
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		writer.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		writer.Header().Set("ETag", `"fixture"`)
		writer.Header().Set("X-Amz-Version-Id", fixture.versions[key])
		_, _ = writer.Write(payload)
	case http.MethodDelete:
		delete(fixture.objects, key)
		delete(fixture.retention, key)
		delete(fixture.versions, key)
		writer.WriteHeader(http.StatusNoContent)
	default:
		principalErasureS3Error(writer, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}

func (fixture *principalErasureLedgerFixture) writeVersions(writer http.ResponseWriter, prefix string) {
	type version struct {
		Key          string `xml:"Key"`
		VersionID    string `xml:"VersionId"`
		IsLatest     bool   `xml:"IsLatest"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int    `xml:"Size"`
		StorageClass string `xml:"StorageClass"`
	}
	type result struct {
		XMLName        xml.Name  `xml:"ListVersionsResult"`
		Xmlns          string    `xml:"xmlns,attr"`
		Name           string    `xml:"Name"`
		Prefix         string    `xml:"Prefix"`
		MaxKeys        int       `xml:"MaxKeys"`
		IsTruncated    bool      `xml:"IsTruncated"`
		ObjectVersions []version `xml:"Version"`
	}
	keys := make([]string, 0, len(fixture.objects))
	for key := range fixture.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	response := result{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", Name: "erasure-test", Prefix: prefix,
		MaxKeys: 1000,
	}
	for _, key := range keys {
		response.ObjectVersions = append(response.ObjectVersions, version{
			Key: key, VersionID: fixture.versions[key], IsLatest: true,
			LastModified: time.Now().UTC().Format(time.RFC3339), ETag: `"fixture"`,
			Size: len(fixture.objects[key]), StorageClass: "STANDARD",
		})
	}
	payload, _ := xml.Marshal(response)
	writer.Header().Set("Content-Type", "application/xml")
	_, _ = writer.Write(payload)
}

func readPrincipalErasureS3Body(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(body)
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
		size, err := strconv.ParseInt(strings.SplitN(strings.TrimSpace(header), ";", 2)[0], 16, 64)
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

func principalErasureS3Error(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/xml")
	writer.WriteHeader(status)
	_, _ = fmt.Fprintf(writer, `<Error><Code>%s</Code><Message>fixture</Message></Error>`, code)
}

func requirePrincipalErasureObligationCount(t *testing.T, fixture *principalErasureLedgerFixture, expected int) {
	t.Helper()
	require.Equal(t, expected, fixture.obligationCount())
}

func TestListErasureObligationsDoesNotRequireDatabaseConfiguration(t *testing.T) {
	fixture := newPrincipalErasureLedgerFixture(t)
	config := AppConfig{}
	fixture.configure(&config)
	auditConfig := iamAuditConfig(config)
	store, err := iamaudit.NewAnchorStore(auditConfig.Anchor)
	require.NoError(t, err)
	obligation := iamaudit.NewErasureObligation(t.Context(), 9201, 3, time.Now().UTC())
	require.NoError(t, iamaudit.NewErasureLedger(store).Put(t.Context(), obligation))

	listed, err := (&App{}).ListErasureObligations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, obligation, listed[0].ErasureObligation)
}
