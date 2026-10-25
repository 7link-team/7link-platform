package clientwork

import (
	"context"
	"encoding/json"
	"testing"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSubmissionPublicationUsesCanonicalSourcesOnceAndFailsClosed(t *testing.T) {
	m, db, _, _ := harness(t)
	realIntakeSources(t, m, db)
	manifest := packageManifest{Refs: []delivery.ClientArtifactRef{{WorkItemID: 11}, {WorkItemID: 12}}}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	row := model.DeliveryPackage{TenantID: 1, ProjectID: 3, ID: 70, Title: "Published", State: "PUBLISHED", Manifest: string(raw), ContentHash: "hash", RecipientID: 4, ActorID: 2, Version: 2, CreatedAt: now(), UpdatedAt: now()}
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Create(&row).Error)
	m.packageSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
		return []delivery.PackageSource{{WorkItemID: 11, IntakeID: 50}, {WorkItemID: 12, IntakeID: 50}}, nil
	}
	call := func() error {
		return db.Transaction(func(tx *gorm.DB) error { return m.publishSubmissionDeliveriesOn(testContext(t, 2), tx, 3, 70) })
	}
	require.NoError(t, call())
	var count int64
	require.NoError(t, db.Table("audit_probe").Where("kind='intake.delivery_published'").Count(&count).Error)
	require.EqualValues(t, 1, count, "two artifacts from the same source need one notification")
	m.packageSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
		return []delivery.PackageSource{{WorkItemID: 99, IntakeID: 50}, {WorkItemID: 12, IntakeID: 50}}, nil
	}
	require.ErrorIs(t, call(), ErrConflict)
	require.NoError(t, db.Table("audit_probe").Where("kind='intake.delivery_published'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	m.packageSources.ReadOn = nil
	require.ErrorIs(t, call(), ErrUnavailable)
}

func TestSubmissionPublicationReplayDoesNotDuplicateCallbacks(t *testing.T) {
	m, db, _, _ := harness(t)
	realIntakeSources(t, m, db)
	m.packageSources.ReadOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, refs []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
		facts := make([]delivery.PackageSource, len(refs))
		for i, ref := range refs {
			facts[i] = delivery.PackageSource{WorkItemID: ref.WorkItemID, IntakeID: 50}
		}
		return facts, nil
	}
	created, err := m.create_packageHTTP(testContext(t, 2), &CreateInput[PackageBody]{EntityID: "3", Key: "notify-package", Body: PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
	require.NoError(t, err)
	in := &EditInput[PackagePublishBody]{Body: PackagePublishBody{ReauthProof: "test-publish-proof"}, EntityID: "3", ID: created.Body.Data.ID.String(), IfMatch: `"1"`, Key: "publish-once"}
	first, err := publishBuiltPackageHTTP(t, m, testContext(t, 2), in)
	require.NoError(t, err)
	retry, err := m.package_publishHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, first.Body.Data, retry.Body.Data)
	require.Equal(t, "true", retry.Replayed)
	var count int64
	require.NoError(t, db.Table("audit_probe").Where("kind='intake.delivery_published'").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
