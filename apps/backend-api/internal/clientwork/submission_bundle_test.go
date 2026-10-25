package clientwork

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func bundleFixture(t *testing.T) (*Module, *gorm.DB, *testProjects, *SubmissionBundleInput, submissionBundleClaims) {
	t.Helper()
	m, db, projects, _ := clarificationHarness(t)
	m.config.SubmissionBundles.SigningKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Updates(map[string]any{"submitted_by_principal_id": 2, "status": "CONVERTED"}).Error)
	manifest := packageManifest{Content: PackageContent{Title: "Mixed", Artifacts: []delivery.ClientArtifact{{Name: "own.txt", VersionID: 91, SHA256: bundleBytesHash("own")}, {Name: "FOREIGN-PRIVATE", VersionID: 92, SHA256: bundleBytesHash("FOREIGN-ORIGINAL-BYTES")}}}, Refs: []delivery.ClientArtifactRef{{WorkItemID: 11}, {WorkItemID: 12}}, Bindings: []packageFile{{VersionID: 91, BindingID: 901}, {VersionID: 92, BindingID: 902}}}
	manifest.Bundle = &packageBundle{PackageBundleView: PackageBundleView{State: "BUILT", Manifest: json.RawMessage(`{"items":[{"work_item_id":"11","title":"own internal work"},{"work_item_id":"12","title":"FOREIGN-PRIVATE"}]}`)}, ContributorLabel: "Team"}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	row := model.DeliveryPackage{TenantID: 1, ProjectID: 3, ID: 70, Title: "Mixed", State: "PUBLISHED", Manifest: string(raw), ContentHash: bundleDigest(manifest.Content), RecipientID: 4, ActorID: 2, Version: 2, CreatedAt: now(), UpdatedAt: now()}
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Create(&row).Error)
	m.packageSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
		return []delivery.PackageSource{{WorkItemID: 11, IntakeID: 50}, {WorkItemID: 12, IntakeID: 999}}, nil
	}
	m.files.OpenOn = func(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedResourceRead, error) {
		require.EqualValues(t, 901, id, "mixed-source pin must never be opened")
		err := m.packageResourceOn(ctx, tx, 70, files.ResourceOwner{Scope: "entity", ID: 3})
		if err != nil {
			return files.PinnedResourceRead{}, err
		}
		return files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 901, ResourceID: 70, Purpose: packagePurpose, ResourceKind: "client_package", VersionID: 91, SHA256: bundleBytesHash("own"), MIME: "text/plain", Size: 3}, Reader: io.NopCloser(strings.NewReader("own"))}, nil
	}
	view, err := m.getSubmissionHTTP(testContext(t, 2), &SubmissionObjectInput{ID: "50"})
	require.NoError(t, err)
	require.True(t, view.Body.Data.DownloadsAvailable)
	u, err := url.Parse(view.Body.Data.Deliveries[0].DownloadURL)
	require.NoError(t, err)
	in := &SubmissionBundleInput{ID: "50", DeliveryID: "70", Token: u.Query().Get("token")}
	c, err := m.verifySubmissionBundle(testContext(t, 2), in, now())
	require.NoError(t, err)
	return m, db, projects, in, c
}
func bundleBytesHash(s string) string { // Hash actual bytes, independently of JSON manifest hashes.
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
func TestSubmissionBundleSignedZIPIsSourceIsolatedAndCleaned(t *testing.T) {
	m, _, _, in, _ := bundleFixture(t)
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	t.Setenv("TMP", scratch)
	t.Setenv("TEMP", scratch)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "bundle.test", Method: "GET", Path: "/bundle"}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return m.submissionBundleHTTP(testContext(t, 2), in)
	})
	response := api.Get("/bundle")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	reader, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	require.NoError(t, err)
	require.Len(t, reader.File, 4)
	for _, file := range reader.File {
		r, err := file.Open()
		require.NoError(t, err)
		raw, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		require.NotContains(t, string(raw), "FOREIGN")
		require.NotContains(t, string(raw), "FOREIGN-ORIGINAL-BYTES")
		require.NotContains(t, string(raw), "binding_id")
		require.NotContains(t, string(raw), "work_item_id")
		if strings.HasPrefix(file.Name, "evidence/") {
			require.Equal(t, "own", string(raw))
		}
	}
	leftovers, err := filepath.Glob(filepath.Join(scratch, "submission-bundle-*"))
	require.NoError(t, err)
	require.Empty(t, leftovers)
}
func TestSubmissionBundleRejectsTamperingExpiryRevocationAndArchive(t *testing.T) {
	m, db, projects, in, c := bundleFixture(t)
	ctx := testContext(t, 2)
	_, err := m.verifySubmissionBundle(ctx, in, c.Issued.Add(72*time.Hour))
	require.NoError(t, err)
	_, err = m.verifySubmissionBundle(ctx, in, c.Issued.Add(73*time.Hour))
	require.ErrorIs(t, err, ErrNotFound)
	_, err = m.verifySubmissionBundle(ctx, in, c.Issued.Add(-time.Second))
	require.Error(t, err)
	for _, bad := range []SubmissionBundleInput{{ID: "51", DeliveryID: "70", Token: in.Token}, {ID: "50", DeliveryID: "71", Token: in.Token}, {ID: "50", DeliveryID: "70", Token: in.Token + "a"}, {ID: "50", DeliveryID: "70", Token: "!bad.mac"}, {ID: "50", DeliveryID: "70", Token: "missing"}} {
		_, err = m.verifySubmissionBundle(ctx, &bad, now())
		require.Error(t, err)
	}
	_, err = m.verifySubmissionBundle(testContext(t, 4), in, now())
	require.Error(t, err)
	for _, stage := range []string{"owner", "project", "archived", "draft", "hash", "content", "source", "pin", "bytes", "storage", "no_reader", "facts"} {
		t.Run(stage, func(t *testing.T) {
			copyModule := *m
			originalOpen := m.files.OpenOn
			switch stage {
			case "owner":
				require.NoError(t, db.Table("g_intake_records").Where("id=50").Update("submitted_by_principal_id", 9).Error)
				defer db.Table("g_intake_records").Where("id=50").Update("submitted_by_principal_id", 2)
			case "project":
				projects.deny = true
				defer func() { projects.deny = false }()
			case "archived":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=70").Update("archived_at", now()).Error)
				defer db.Table(m.names.DeliveryPackage.String()).Where("id=70").Update("archived_at", nil)
			case "draft":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=70").Update("state", "DRAFT").Error)
				defer db.Table(m.names.DeliveryPackage.String()).Where("id=70").Update("state", "PUBLISHED")
			case "hash":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=70").Update("content_hash", strings.Repeat("0", 64)).Error)
				defer db.Table(m.names.DeliveryPackage.String()).Where("id=70").Update("content_hash", c.PackageHash)
			case "content":
				var row model.DeliveryPackage
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=70").Take(&row).Error)
				var manifest packageManifest
				require.NoError(t, json.Unmarshal([]byte(row.Manifest), &manifest))
				manifest.Content.Title = "tampered"
				raw, _ := json.Marshal(manifest)
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=70").Update("manifest", string(raw)).Error)
				defer db.Table(m.names.DeliveryPackage.String()).Where("id=70").Update("manifest", row.Manifest)
			case "source":
				copyModule.packageSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
					return []delivery.PackageSource{{WorkItemID: 11, IntakeID: 999}, {WorkItemID: 12, IntakeID: 999}}, nil
				}
			case "facts":
				copyModule.packageSources.ReadOn = nil
			default:
				copyModule.files.OpenOn = func(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedResourceRead, error) {
					r, e := originalOpen(ctx, tx, id)
					if e != nil {
						return r, e
					}
					switch stage {
					case "pin":
						r.ResourceID = 999
					case "bytes":
						_ = r.Reader.Close()
						r.Reader = io.NopCloser(strings.NewReader("BAD"))
					case "storage":
						return r, errors.New("offline")
					case "no_reader":
						_ = r.Reader.Close()
						r.Reader = nil
					}
					return r, nil
				}
			}
			scratch := t.TempDir()
			t.Setenv("TMPDIR", scratch)
			t.Setenv("TMP", scratch)
			t.Setenv("TEMP", scratch)
			response, err := copyModule.submissionBundleHTTP(ctx, in)
			require.Error(t, err)
			require.Nil(t, response)
			entries, err := os.ReadDir(scratch)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		// No grant means the ordinary package callback still requires delivery_read.
		require.Error(t, m.packageResourceOn(testContext(t, 5), tx, 70, files.ResourceOwner{Scope: "entity", ID: 3}))
		for _, grant := range []submissionBundleGrant{{claims: c, binding: 902}, {claims: c, binding: 999}} {
			grantCtx := context.WithValue(ctx, submissionBundleGrantKey{}, grant)
			require.Error(t, m.packageResourceOn(grantCtx, tx, 70, files.ResourceOwner{Scope: "entity", ID: 3}))
			require.Error(t, m.packageResourceOn(grantCtx, tx, 71, files.ResourceOwner{Scope: "entity", ID: 3}))
			require.Error(t, m.packageResourceOn(grantCtx, tx, 70, files.ResourceOwner{Scope: "tenant", ID: 3}))
		}
		return nil
	}))
	m.config.SubmissionBundles.SigningKey = ""
	_, err = m.submissionBundleKeys()
	require.ErrorIs(t, err, ErrUnavailable)
	m.config.SubmissionBundles.SigningKey = "invalid"
	_, err = m.submissionBundleKeys()
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestSubmissionBundleKeyRotationAndSignedClaimBoundaries(t *testing.T) {
	m, _, _, in, claims := bundleFixture(t)
	ctx := testContext(t, 2)
	old := m.config.SubmissionBundles.SigningKey
	m.config.SubmissionBundles.SigningKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{43}, 32))
	m.config.SubmissionBundles.PreviousSigningKeys = []string{old}
	_, err := m.verifySubmissionBundle(ctx, in, now())
	require.NoError(t, err)
	m.config.SubmissionBundles.PreviousSigningKeys = nil
	_, err = m.verifySubmissionBundle(ctx, in, now())
	require.Error(t, err)
	m.config.SubmissionBundles.SigningKey = old
	ring, err := m.submissionBundleKeys()
	require.NoError(t, err)
	for _, raw := range [][]byte{[]byte("broken"), func() []byte {
		snapshot := claims
		snapshot.Expires = snapshot.Expires.Add(time.Hour)
		raw, _ := json.Marshal(snapshot)
		return raw
	}()} {
		snapshot := *in
		snapshot.Token = base64.RawURLEncoding.EncodeToString(raw) + "." + ring.MAC("download", raw)
		_, err = m.verifySubmissionBundle(ctx, &snapshot, now())
		require.Error(t, err)
	}
	otherClaims, _ := authn.FromContext(ctx)
	copyClaims := *otherClaims
	copyClaims.TenantID = 8
	_, err = m.verifySubmissionBundle(authn.WithClaims(ctx, &copyClaims), in, now())
	require.Error(t, err)
	m.config.SubmissionBundles.SigningKey = "invalid"
	_, err = m.submissionBundleHTTP(ctx, in)
	require.Error(t, err)
	err = m.signSubmissionBundle(ctx, 50, &model.DeliveryPackage{}, &SubmissionDelivery{}, now())
	require.ErrorIs(t, err, ErrUnavailable)
}

type failingBundleReader struct{}

func TestSubmissionBundleRequiresActualSuccessfulBuildReceipt(t *testing.T) {
	m, db, _, input, _ := bundleFixture(t)
	q := query.BindDeliveryPackage(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(guid.ID(70))).First()
	require.NoError(t, err)
	var manifest packageManifest
	require.NoError(t, json.Unmarshal([]byte(row.Manifest), &manifest))
	manifest.Bundle = nil
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(guid.ID(70))).UpdateSimple(q.Manifest.Value(string(raw)))
	require.NoError(t, err)
	view, err := m.getSubmissionHTTP(testContext(t, 2), &SubmissionObjectInput{ID: "50"})
	require.NoError(t, err)
	require.False(t, view.Body.Data.DownloadsAvailable)
	require.Empty(t, view.Body.Data.Deliveries[0].DownloadURL)
	_, err = m.submissionBundleHTTP(testContext(t, 2), input)
	require.Error(t, err, "a cached signed source URL cannot restore unavailable redacted output")
}

func (failingBundleReader) Read([]byte) (int, error) { return 0, errors.New("object read failed") }
func (failingBundleReader) Close() error             { return nil }

func TestSubmissionBundleMissingPinsReadFailuresAndUnavailableStorageFailClosed(t *testing.T) {
	m, db, _, in, c := bundleFixture(t)
	ctx := testContext(t, 2)
	for _, stage := range []string{"source_not_converted", "package_missing", "missing_pin", "conflicting_pin", "read_failure", "no_file_provider", "temporary_storage", "bad_signing_config"} {
		t.Run(stage, func(t *testing.T) {
			copyModule := *m
			var row model.DeliveryPackage
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=70").Take(&row).Error)
			switch stage {
			case "source_not_converted":
				require.NoError(t, db.Table("g_intake_records").Where("id=50").Update("status", "RECEIVED").Error)
				defer db.Table("g_intake_records").Where("id=50").Update("status", "CONVERTED")
			case "package_missing":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=70").Delete(&model.DeliveryPackage{}).Error)
				defer db.Table(m.names.DeliveryPackage.String()).Create(&row)
			case "missing_pin", "conflicting_pin":
				var manifest packageManifest
				require.NoError(t, json.Unmarshal([]byte(row.Manifest), &manifest))
				if stage == "missing_pin" {
					manifest.Bindings = nil
				} else {
					manifest.Bindings = append(manifest.Bindings, packageFile{VersionID: 91, BindingID: 903})
				}
				raw, _ := json.Marshal(manifest)
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=70").Update("manifest", string(raw)).Error)
				defer db.Table(m.names.DeliveryPackage.String()).Where("id=70").Update("manifest", row.Manifest)
			case "read_failure":
				original := copyModule.files.OpenOn
				copyModule.files.OpenOn = func(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedResourceRead, error) {
					r, e := original(ctx, tx, id)
					if r.Reader != nil {
						_ = r.Reader.Close()
					}
					r.Reader = failingBundleReader{}
					return r, e
				}
			case "no_file_provider":
				copyModule.files.OpenOn = nil
			case "temporary_storage":
				scratch := filepath.Join(t.TempDir(), "absent")
				t.Setenv("TMPDIR", scratch)
				t.Setenv("TMP", scratch)
				t.Setenv("TEMP", scratch)
			case "bad_signing_config":
				copyModule.config.SubmissionBundles.SigningKey = "invalid"
				_, err := copyModule.getSubmissionHTTP(ctx, &SubmissionObjectInput{ID: "50"})
				require.Error(t, err)
			}
			response, err := copyModule.submissionBundleHTTP(ctx, in)
			require.Error(t, err)
			require.Nil(t, response)
		})
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		bad := c
		bad.Package = 999
		_, err := m.loadSubmissionBundlePackageOn(ctx, tx, bad)
		require.Error(t, err)
		bad = c
		bad.Submission = 999
		grantCtx := context.WithValue(ctx, submissionBundleGrantKey{}, submissionBundleGrant{claims: bad, binding: 901})
		require.Error(t, m.packageResourceOn(grantCtx, tx, 70, files.ResourceOwner{Scope: "entity", ID: 3}))
		return nil
	}))
}
