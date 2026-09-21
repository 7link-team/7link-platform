package audit

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErasureLedgerPersistsVerifiesAndListsIdempotentObligations(t *testing.T) {
	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "erasure-ledger")
	ledger := NewErasureLedger(store)
	ctx := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: 7001})
	obligation := NewErasureObligation(ctx, 9001, 4, auditTestNow)

	require.NoError(t, ledger.Put(ctx, obligation))
	require.NoError(t, ledger.Put(ctx, obligation), "a retry must verify rather than overwrite the obligation")
	listed, err := ledger.List(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, obligation, listed[0].ErasureObligation)
	assert.Equal(t, erasureObligationKey(9001, 4), listed[0].ObjectKey)
	assert.NotEmpty(t, listed[0].VersionID)
	assert.Len(t, listed[0].SHA256, 64)
	assert.True(t, listed[0].RetainUntil.After(time.Now().UTC().Add(29*24*time.Hour)))

	payload := fixture.objects[listed[0].ObjectKey]
	assert.NotContains(t, string(payload), "email")
	assert.NotContains(t, string(payload), "login")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(payload, &decoded))
	assert.ElementsMatch(t,
		[]string{"schema", "principal_id", "lifecycle_version", "requested_by", "recorded_at"},
		mapKeys(decoded),
	)
	putCount := 0
	for _, request := range fixture.requestPaths {
		if strings.HasPrefix(request, "PUT /erasure-ledger/"+erasureObligationPrefix+"/") {
			putCount++
		}
	}
	assert.Equal(t, 1, putCount)
}

func TestListErasureObligationsUsesOnlyExternalConfiguration(t *testing.T) {
	fixture := newFakeS3()
	store, server := fakeAnchorStore(t, fixture, "erasure-recovery")
	obligation := NewErasureObligation(t.Context(), 8101, 6, auditTestNow)
	require.NoError(t, NewErasureLedger(store).Put(t.Context(), obligation))
	config := Config{
		Anchor: AnchorConfig{
			Endpoint: server.URL, Bucket: "erasure-recovery", AccessKey: "access",
			SecretKey: "secret", RetentionDays: 30,
		},
		ErasureLedger: ErasureLedgerConfig{Enabled: true, Timeout: 5 * time.Second},
	}

	listed, err := ListErasureObligations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, obligation, listed[0].ErasureObligation)
	assert.ErrorIs(t, func() error {
		_, listErr := ListErasureObligations(t.Context(), Config{})
		return listErr
	}(), ErrErasureLedgerDisabled)
}

func TestErasureLedgerFailsClosedForInvalidUnavailableAndCorruptObjects(t *testing.T) {
	assert.ErrorIs(t, (*ErasureLedger)(nil).Check(t.Context()), ErrErasureLedgerDisabled)
	assert.ErrorIs(t, (*ErasureLedger)(nil).Put(t.Context(), ErasureObligation{}), ErrErasureLedgerDisabled)

	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "erasure-failures")
	ledger := NewErasureLedger(store)
	assert.ErrorIs(t, ledger.Put(t.Context(), ErasureObligation{}), ErrErasureLedgerInvalid)

	fixture.fail = true
	err := ledger.Put(t.Context(), NewErasureObligation(t.Context(), 1, 1, auditTestNow))
	assert.ErrorIs(t, err, ErrErasureLedgerUnavailable)
	fixture.fail = false

	obligation := NewErasureObligation(t.Context(), 2, 3, auditTestNow)
	require.NoError(t, ledger.Put(t.Context(), obligation))
	key := erasureObligationKey(2, 3)
	fixture.objects[key] = []byte(`{"schema":"iam-erasure-obligation.v1","principal_id":99}`)
	_, err = ledger.List(t.Context())
	assert.ErrorIs(t, err, ErrErasureLedgerIntegrity)

	fixture.objects[key], _ = json.Marshal(obligation)
	fixture.retention[key] = auditTestNow.Add(-24 * time.Hour).Format(time.RFC3339)
	listed, listErr := ledger.List(t.Context())
	require.NoError(t, listErr, "an expired but still present obligation remains recovery evidence")
	require.Len(t, listed, 1)

	fixture.lockModes[key] = string(minio.Governance)
	_, err = ledger.List(t.Context())
	assert.ErrorIs(t, err, ErrErasureLedgerIntegrity)
}

func TestErasureLedgerConfigBoundsSynchronousWrites(t *testing.T) {
	assert.Error(t, validateErasureLedgerConfig(ErasureLedgerConfig{}))
	assert.Error(t, validateErasureLedgerConfig(ErasureLedgerConfig{Timeout: 61 * time.Second}))
	assert.NoError(t, validateErasureLedgerConfig(ErasureLedgerConfig{Timeout: 10 * time.Second}))
}

func TestErasureLedgerRealObjectLock(t *testing.T) {
	endpoint := os.Getenv("SEVENLINK_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("real MinIO endpoint not configured; set SEVENLINK_MINIO_ENDPOINT")
	}
	address, secure, err := endpointAddress(endpoint)
	require.NoError(t, err)
	client, err := minio.New(address, &minio.Options{
		Creds: credentials.NewStaticV4(auditMinIOTestUser, auditMinIOTestPassword, ""), Secure: secure,
	})
	require.NoError(t, err)
	bucket := "erasure-ledger-" + time.Now().UTC().Format("20060102-150405.000000000")
	store, err := NewAnchorStore(AnchorConfig{
		Endpoint: endpoint, Bucket: bucket, AccessKey: auditMinIOTestUser,
		SecretKey: auditMinIOTestPassword, RetentionDays: 1,
	})
	require.NoError(t, err)
	ledger := NewErasureLedger(store)
	require.NoError(t, ledger.Put(t.Context(), NewErasureObligation(t.Context(), 5001, 2, time.Now().UTC())))
	listed, err := ledger.List(t.Context())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	mode, until, err := client.GetObjectRetention(t.Context(), bucket, listed[0].ObjectKey, listed[0].VersionID)
	require.NoError(t, err)
	require.NotNil(t, mode)
	require.NotNil(t, until)
	assert.Equal(t, minio.Compliance, *mode)
	require.NoError(t, client.RemoveObject(t.Context(), bucket, listed[0].ObjectKey, minio.RemoveObjectOptions{}),
		"an unversioned delete creates a marker but cannot remove the locked version")
	listedBehindMarker, err := ledger.List(t.Context())
	require.NoError(t, err)
	require.Len(t, listedBehindMarker, 1)
	assert.Equal(t, listed[0].VersionID, listedBehindMarker[0].VersionID)
	require.NoError(t, ledger.Put(t.Context(), NewErasureObligation(t.Context(), 5001, 2, time.Now().UTC())),
		"a retry behind a delete marker must recover the first locked version")
	listedAfterRetry, err := ledger.List(t.Context())
	require.NoError(t, err)
	require.Len(t, listedAfterRetry, 1)
	assert.Equal(t, listed[0].VersionID, listedAfterRetry[0].VersionID)
	err = client.RemoveObject(t.Context(), bucket, listed[0].ObjectKey, minio.RemoveObjectOptions{VersionID: listed[0].VersionID})
	assert.Error(t, err, "a compliance-locked erasure obligation must refuse deletion")
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

// TestErasureLedgerVerifyRejectsUnreadableAndTamperedVersions pins the checks a
// stored object has to pass before the platform treats it as the erasure
// obligation it owes. Each case fails on its own: the object is only evidence
// when the exact version the ledger names is readable, small enough to be an
// obligation, the digest the writer computed, exactly one JSON document, and
// still under a compliance lock. A verify that stopped enforcing any one of
// them would accept a replaced or truncated object as the original.
func TestErasureLedgerVerifyRejectsUnreadableAndTamperedVersions(t *testing.T) {
	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "erasure-verify")
	ledger := NewErasureLedger(store)
	obligation := NewErasureObligation(t.Context(), 4401, 2, auditTestNow)
	require.NoError(t, ledger.Put(t.Context(), obligation))
	key := erasureObligationKey(4401, 2)
	version := fixture.versions[key]
	require.NotEmpty(t, version)
	stored := append([]byte(nil), fixture.objects[key]...)

	_, err := ledger.verify(t.Context(), key, "null", "", time.Time{})
	assert.ErrorIs(t, err, ErrErasureLedgerIntegrity, "an unversioned object is not a pinned obligation")

	fixture.failMethod, fixture.failContains = http.MethodHead, key
	_, err = ledger.verify(t.Context(), key, version, "", time.Time{})
	assert.ErrorIs(t, err, ErrErasureLedgerUnavailable, "a failed stat is unavailability, not a clean obligation")

	fixture.failMethod, fixture.failContains = http.MethodGet, key+"?versionId="
	_, err = ledger.verify(t.Context(), key, version, "", time.Time{})
	assert.ErrorIs(t, err, ErrErasureLedgerUnavailable, "an unreadable body is unavailability, not a clean obligation")

	fixture.failMethod, fixture.failContains = http.MethodGet, "retention"
	_, err = ledger.verify(t.Context(), key, version, "", time.Time{})
	assert.ErrorIs(t, err, ErrErasureLedgerUnavailable, "an unreadable lock cannot be assumed to be compliance")
	fixture.failMethod, fixture.failContains = "", ""

	_, err = ledger.verify(t.Context(), key, version, strings.Repeat("0", 64), time.Time{})
	assert.ErrorIs(t, err, ErrErasureLedgerIntegrity, "the stored bytes must hash to the digest the writer computed")

	for name, payload := range map[string][]byte{
		"oversized":        make([]byte, maxErasureObligationSize+1),
		"not json":         []byte(`{"schema":`),
		"trailing content": append(append([]byte(nil), stored...), []byte("\n{}")...),
	} {
		t.Run(name, func(t *testing.T) {
			fixture.objects[key] = payload
			_, verifyErr := ledger.verify(t.Context(), key, version, "", time.Time{})
			assert.ErrorIs(t, verifyErr, ErrErasureLedgerIntegrity)
		})
	}
	fixture.objects[key] = stored
	verified, err := ledger.verify(t.Context(), key, version, "", time.Time{})
	require.NoError(t, err, "the untouched object must still verify after every rejected variant")
	assert.Equal(t, obligation, verified.ErasureObligation)
}

// TestErasureLedgerPutRefusesToRepublishOverAFaultyPriorObject pins what a
// retry does when the ledger already holds something for this lifecycle
// version. A retry must recover the object that is there, never quietly write
// a second one, and a store that refuses the write has to be reported as
// unavailable once the retry has confirmed nothing landed.
func TestErasureLedgerPutRefusesToRepublishOverAFaultyPriorObject(t *testing.T) {
	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "erasure-put")
	ledger := NewErasureLedger(store)
	obligation := NewErasureObligation(t.Context(), 4501, 3, auditTestNow)
	key := erasureObligationKey(4501, 3)
	// A neighboring key shares this obligation's listing prefix; it must not
	// be mistaken for the obligation itself.
	fixture.objects[key+".sidecar"] = []byte("unrelated")
	require.NoError(t, ledger.Put(t.Context(), obligation))
	published := fixture.versions[key]
	require.NotEmpty(t, published)
	require.NoError(t, ledger.Put(t.Context(), obligation))
	assert.Equal(t, published, fixture.versions[key], "the retry must recover the first version, not write a second")

	fixture.objects[key] = []byte(`{"schema":"iam-erasure-obligation.v1"`)
	assert.ErrorIs(t, ledger.Put(t.Context(), obligation), ErrErasureLedgerIntegrity,
		"a retry over an unreadable prior object must stop rather than overwrite it")
	fixture.objects[key], _ = json.Marshal(obligation)

	refused := erasureObligationKey(4502, 1)
	fixture.failMethod, fixture.failContains = http.MethodPut, refused
	err := ledger.Put(t.Context(), NewErasureObligation(t.Context(), 4502, 1, auditTestNow))
	fixture.failMethod, fixture.failContains = "", ""
	assert.ErrorIs(t, err, ErrErasureLedgerUnavailable)
	assert.NotContains(t, fixture.objects, refused, "a refused write must leave no obligation behind")
}
