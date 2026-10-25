package clientwork

import (
	"context"
	"fmt"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPackageDraftEditingRevalidatesPinsAndFreezesAtPublication(t *testing.T) {
	m, db, projects, _ := harness(t)
	ctx := testContext(t, 2)
	var pinned, unpinned []guid.ID
	pin := m.files.PinOn
	m.files.PinOn = func(ctx context.Context, tx *gorm.DB, in files.ResourceBindingInput) (files.PinnedResource, error) {
		pinned = append(pinned, *in.VersionID)
		return pin(ctx, tx, in)
	}
	m.files.UnpinOn = func(_ context.Context, tx *gorm.DB, binding, packageID guid.ID) error {
		unpinned = append(unpinned, binding)
		return tx.Exec("INSERT INTO baseline_probe VALUES (?)", binding).Error
	}
	body := PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}, {WorkItemID: 11, ResultID: 20, EvidenceID: 32}}}
	created, e := m.create_packageHTTP(ctx, &CreateInput[PackageBody]{EntityID: "3", Key: "compose", Body: body})
	require.NoError(t, e)
	require.Equal(t, []guid.ID{40}, pinned, "two criteria on one file pin the immutable version once")
	id := created.Body.Data.ID
	before, e := m.get_packageHTTP(ctx, &ObjectInput{EntityID: "3", ID: id.String()})
	require.NoError(t, e)
	require.NotNil(t, before.Body.Data.Draft)
	body.Title, body.RecipientID = "Edited release", 5
	body.Artifacts = []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 31}}
	input := &EditInput[PackageBody]{EntityID: "3", ID: id.String(), Key: "edit", IfMatch: "\"1\"", Body: body}
	projects.stale = true
	_, e = m.editPackageHTTP(ctx, input)
	require.Error(t, e)
	projects.stale = false
	publisher := m.events
	m.events = failPublisher{}
	_, e = m.editPackageHTTP(ctx, input)
	require.Error(t, e)
	var count int64
	require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
	require.Zero(t, count, "failed edits roll back file unbinding")
	m.events = publisher
	edited, e := m.editPackageHTTP(ctx, input)
	require.NoError(t, e)
	require.Equal(t, 2, edited.Body.Data.Version)
	replayed, e := m.editPackageHTTP(ctx, input)
	require.NoError(t, e)
	require.Equal(t, "true", replayed.Replayed)
	after, e := m.get_packageHTTP(ctx, &ObjectInput{EntityID: "3", ID: id.String()})
	require.NoError(t, e)
	require.NotEqual(t, before.Body.Data.ContentHash, after.Body.Data.ContentHash)
	require.Equal(t, guid.ID(5), after.Body.Data.Draft.RecipientID)
	require.NotEmpty(t, unpinned)
	input.Key = "stale-edit"
	_, e = m.editPackageHTTP(ctx, input)
	require.Error(t, e)
	_, e = m.editPackageHTTP(testContext(t, 4), input)
	require.Error(t, e)
	_, e = publishBuiltPackageHTTP(t, m, ctx, &EditInput[PackagePublishBody]{Body: PackagePublishBody{ReauthProof: "test-publish-proof"}, EntityID: "3", ID: id.String(), Key: "publish", IfMatch: "\"2\""})
	require.NoError(t, e)
	input.IfMatch = "\"3\""
	input.Key = "published-edit"
	_, e = m.editPackageHTTP(ctx, input)
	require.Error(t, e)
	public, e := m.get_packageHTTP(testContext(t, 5), &ObjectInput{EntityID: "3", ID: id.String()})
	require.NoError(t, e)
	require.Nil(t, public.Body.Data.Draft, "public packages never expose internal source references")
	input.EntityID = "bad"
	_, e = m.editPackageHTTP(ctx, input)
	require.Error(t, e)
}

func TestPackageDraftRetainsUnchangedPinsAndRejectsMissingUnpinCapability(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	artifacts := []delivery.ClientArtifact{{VersionID: 40}}
	refs := []delivery.ClientArtifactRef{{EvidenceID: 30}}
	previous := []packageFile{{VersionID: 40, BindingID: 101}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		pins, e := m.packagePinsOn(ctx, tx, 3, 100, refs, artifacts, previous)
		require.NoError(t, e)
		require.Equal(t, previous, pins)
		previous = append(previous, packageFile{VersionID: 41, BindingID: 102})
		_, e = m.packagePinsOn(ctx, tx, 3, 100, refs, artifacts, previous)
		require.ErrorIs(t, e, ErrUnavailable)
		_, e = m.packagePinsOn(ctx, tx, 3, 100, nil, artifacts, nil)
		require.ErrorIs(t, e, ErrInvalid)
		m.files.UnpinOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return fmt.Errorf("unbind failed") }
		_, e = m.packagePinsOn(ctx, tx, 3, 100, refs, artifacts, previous)
		require.Error(t, e)
		return nil
	}))
}
