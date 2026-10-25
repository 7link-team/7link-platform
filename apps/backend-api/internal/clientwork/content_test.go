package clientwork

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

type unavailableArtifactFiles struct{ delivery.ClientWorkAccess }

func (unavailableArtifactFiles) ArtifactFileOn(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.ClientArtifactRef) (files.PinnedCandidate, error) {
	return files.PinnedCandidate{}, ErrUnavailable
}

func TestPinnedContentDependencyAndCurrentAuthorityFailuresExposeNoBytes(t *testing.T) {
	m, db, projects, _ := harness(t)
	ctx := testContext(t, 2)
	for _, in := range []*ObjectInput{{EntityID: "bad", ID: "1"}, {EntityID: "3", ID: "bad"}, {EntityID: "3", ID: "999"}} {
		response, err := m.previewContentHTTP(ctx, in)
		require.Error(t, err)
		require.Nil(t, response)
	}
	projects.deny = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.Error(t, m.previewResourceOn(ctx, tx, 99, files.ResourceOwner{Scope: "entity", ID: 3}))
		require.Error(t, m.packageResourceOn(ctx, tx, 99, files.ResourceOwner{Scope: "entity", ID: 3}))
		return nil
	}))
	projects.deny = false
	for _, stage := range []string{"missing_reader", "artifact_lookup", "ids_unavailable"} {
		t.Run(stage, func(t *testing.T) {
			copyModule := *m
			copyModule.files.PinOn = func(context.Context, *gorm.DB, files.ResourceBindingInput) (files.PinnedResource, error) {
				t.Fatal("failed dependency must not create a binding")
				return files.PinnedResource{}, nil
			}
			switch stage {
			case "missing_reader":
				copyModule.files.OpenOn = nil
			case "artifact_lookup":
				copyModule.projects = unavailableArtifactFiles{copyModule.projects}
			case "ids_unavailable":
				copyModule.ids = &unavailableIDs{source: m.ids, at: 1}
			}
			before := collaborationSnapshot(t, m, db)
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := copyModule.pinArtifactOn(ctx, tx, 3, 99, packagePurpose, "client_package", delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30})
				return err
			})
			require.ErrorIs(t, err, ErrUnavailable)
			require.Equal(t, before, collaborationSnapshot(t, m, db))
		})
	}
}

func TestPinnedContentMissingBindingAndFailedOpenCloseReaders(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	var preview Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		preview, err = m.createPreviewOn(ctx, tx, 3, PreviewBody{Title: "Fixed document", Artifact: delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30}, ClientVisible: true})
		return err
	}))
	require.NoError(t, db.Table(m.names.Preview.String()).Where("id=?", preview.ID).Update("binding_id", nil).Error)
	response, err := m.previewContentHTTP(ctx, &ObjectInput{EntityID: "3", ID: preview.ID.String()})
	require.Error(t, err)
	require.Nil(t, response)
	reader := &attachmentReader{Reader: strings.NewReader("private bytes")}
	m.files.OpenOn = func(context.Context, *gorm.DB, guid.ID) (files.PinnedResourceRead, error) {
		return files.PinnedResourceRead{Reader: reader}, errors.New("object open failed after reader allocation")
	}
	response, err = m.streamContent(ctx, 3, func(*gorm.DB, bool) (guid.ID, string, error) { return 901, strings.Repeat("a", 64), nil })
	require.Error(t, err)
	require.Nil(t, response)
	require.True(t, reader.closed)
}

func TestPinnedContentAuthorizationHashAndRevocation(t *testing.T) {
	m, db, _, clients := harness(t)
	ref := delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30}
	var preview Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		preview, e = m.createPreviewOn(testContext(t, 2), tx, 3, PreviewBody{Title: "Document", Artifact: ref, ClientVisible: true})
		return e
	}))
	_, api := humatest.New(t)
	api.UseMiddleware(func(c huma.Context, next func(huma.Context)) { next(huma.WithContext(c, testContext(t, 4))) })
	huma.Register(api, huma.Operation{OperationID: "test.content", Method: "GET", Path: "/content"}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return m.previewContentHTTP(ctx, &ObjectInput{EntityID: "3", ID: preview.ID.String()})
	})
	response := api.Get("/content")
	require.Equal(t, 200, response.Code)
	require.Equal(t, "test", response.Body.String())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	require.Contains(t, response.Header().Get("Content-Disposition"), "attachment")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.previewResourceOn(testContext(t, 4), tx, preview.ID, files.ResourceOwner{Scope: "entity", ID: 3}))
		require.Error(t, m.previewResourceOn(testContext(t, 4), tx, preview.ID, files.ResourceOwner{Scope: "entity", ID: 8}))
		return nil
	}))
	clients.revoked = true
	_, e := m.previewContentHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: preview.ID.String()})
	require.Error(t, e)
	clients.revoked = false
	m.files.OpenOn = func(context.Context, *gorm.DB, guid.ID) (files.PinnedResourceRead, error) {
		return files.PinnedResourceRead{PinnedResource: files.PinnedResource{SHA256: strings.Repeat("b", 64)}}, nil
	}
	_, e = m.previewContentHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: preview.ID.String()})
	require.Error(t, e)
	_, e = m.packageContentHTTP(authn.WithClaims(t.Context(), nil), &packageContentInput{EntityID: "bad"})
	require.Error(t, e)
}

func TestPackageContentPinsManifestRecipientVersionAndCurrentMembership(t *testing.T) {
	m, db, _, clients := harness(t)
	var r Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = m.createPackageOn(testContext(t, 2), tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
		return e
	}))
	input := &packageContentInput{EntityID: "3", ID: r.ID.String(), VersionID: "40"}
	_, e := m.packageContentHTTP(testContext(t, 4), input)
	require.Error(t, e, "private draft cannot be downloaded")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := publishBuiltPackage(t, m, testContext(t, 2), tx, 3, r.ID, r.Version)
		return e
	}))
	stream, e := m.packageContentHTTP(testContext(t, 4), input)
	require.NoError(t, e)
	require.NotNil(t, stream)
	input.VersionID = "41"
	_, e = m.packageContentHTTP(testContext(t, 4), input)
	require.Error(t, e, "arbitrary file version cannot be downloaded")
	input.VersionID = "40"
	_, e = m.packageContentHTTP(testContext(t, 5), input)
	require.Error(t, e, "other client cannot download the package")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.packageResourceOn(testContext(t, 4), tx, r.ID, files.ResourceOwner{Scope: "entity", ID: 3}))
		require.Error(t, m.packageResourceOn(testContext(t, 4), tx, r.ID, files.ResourceOwner{Scope: "tenant", ID: 3}))
		return nil
	}))
	clients.revoked = true
	_, e = m.packageContentHTTP(testContext(t, 4), input)
	require.Error(t, e)
	for _, bad := range []*packageContentInput{{EntityID: "0"}, {EntityID: "3", ID: "bad"}, {EntityID: "3", ID: r.ID.String(), VersionID: "0"}} {
		_, e = m.packageContentHTTP(testContext(t, 4), bad)
		require.Error(t, e)
	}
	m.files.OpenOn = nil
	_, e = m.packageContentHTTP(testContext(t, 2), input)
	require.Error(t, e)
}
