package clientwork

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPackageRejectionCreatesRealReworkSourceAtomicallyAndRetriesMissingSource(t *testing.T) {
	for _, delayed := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "source-added-later"}[delayed], func(t *testing.T) {
			m, db, projects, _ := harness(t)
			realIntakeSources(t, m, db)
			frozenAnchor := execution.ClientThreadAnchor{ThreadID: 90, AnchorKind: "TEXT_PARAGRAPH", AnchorExcerpt: "Frozen rejected paragraph", VersionRef: "file-version:40", ArtifactRef: delivery.ArtifactAnchorSnapshot{Kind: "TEXT_PARAGRAPH", VersionID: 40, Excerpt: "Frozen rejected paragraph"}, Anchor: json.RawMessage(`{"paragraph_index":0,"quote":{"exact":"Frozen rejected paragraph"}}`)}
			m.packageComments.ConvertOn = func(_ context.Context, _ *gorm.DB, _, _, _ guid.ID, _ string, thread, _, _ guid.ID) (execution.ClientThreadAnchor, error) {
				require.EqualValues(t, 90, thread)
				return frozenAnchor, nil
			}
			if !delayed {
				require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
			}
			created, e := m.create_packageHTTP(testContext(t, 2), &CreateInput[PackageBody]{EntityID: "3", Key: "package", Body: PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}, {WorkItemID: 11, ResultID: 20, EvidenceID: 31}}}})
			require.NoError(t, e)
			id := created.Body.Data.ID.String()
			_, e = publishBuiltPackageHTTP(t, m, testContext(t, 2), &EditInput[PackagePublishBody]{Body: PackagePublishBody{ReauthProof: "test-publish-proof"}, EntityID: "3", ID: id, IfMatch: "\"1\"", Key: "publish"})
			require.NoError(t, e)
			view, e := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: id})
			require.NoError(t, e)
			// This fixture checks the rejected subset, after publication's full
			// source discovery has already completed.
			m.packageSources.ReadOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, refs []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
				require.EqualValues(t, 1, tenant)
				require.EqualValues(t, 3, project)
				require.Len(t, refs, 1)
				require.EqualValues(t, 31, refs[0].EvidenceID)
				return []delivery.PackageSource{{WorkItemID: 11, RequirementID: 10, IntakeID: 50}}, nil
			}
			body := PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: view.Body.Data.ContentHash, Reason: "Reviewed files", Items: []PackageItemDecision{{ItemID: view.Body.Data.Items[0].ItemID, Result: "ACCEPTED"}, {ItemID: view.Body.Data.Items[1].ItemID, Result: "REJECTED", FeedbackCategory: "FUNCTIONAL_DEFECT", Comment: "The output has a reproducible defect", CommentThreadIDs: []guid.ID{90}}}}
			request := &EditInput[PackageDecision]{EntityID: "3", ID: id, IfMatch: "\"2\"", Key: "receipt", Body: body}
			// A failure after Intake insertion rolls back source and receipt together.
			publisher := m.events
			m.events = failPublisher{}
			_, e = m.package_acceptanceHTTP(testContext(t, 4), request)
			require.Error(t, e)
			m.events = publisher
			var count int64
			require.NoError(t, db.Table("g_intake_records").Where("kind='REWORK'").Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Table("g_clientwork_package_receipts").Count(&count).Error)
			require.Zero(t, count)
			accepted, e := m.package_acceptanceHTTP(testContext(t, 4), request)
			require.NoError(t, e)
			got, e := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: id})
			require.NoError(t, e)
			retry := &EditInput[ClientworkPublishBody]{EntityID: "3", ID: id, IfMatch: "\"3\"", Key: "create-rework"}
			if delayed {
				require.True(t, got.Body.Data.ReworkUnavailable)
				require.NotEmpty(t, got.Body.Data.ReworkUnavailableReason)
				_, e = m.retryPackageReworkHTTP(testContext(t, 2), retry)
				require.Error(t, e)
				require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
			} else {
				require.NotNil(t, got.Body.Data.ReworkIntakeID)
				require.False(t, got.Body.Data.ReworkUnavailable)
			}
			_, e = m.retryPackageReworkHTTP(testContext(t, 4), retry)
			require.Error(t, e)
			createdRework, e := m.retryPackageReworkHTTP(testContext(t, 2), retry)
			require.NoError(t, e)
			require.Equal(t, 201, createdRework.Status)
			again, e := m.retryPackageReworkHTTP(testContext(t, 2), retry)
			require.NoError(t, e)
			require.Equal(t, "true", again.Replayed)
			retry.Key = "second-rework-key"
			second, e := m.retryPackageReworkHTTP(testContext(t, 2), retry)
			require.NoError(t, e)
			require.Equal(t, createdRework.Body.Data.ID, second.Body.Data.ID)
			source, e := m.getPackageReworkHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: id})
			require.NoError(t, e)
			require.Equal(t, createdRework.Body.Data.ID, source.Body.Data.ID)
			require.Equal(t, "RECEIVED", source.Body.Data.State)
			require.Equal(t, accepted.Body.Data.ID, source.Body.Data.PackageID)
			require.Len(t, source.Body.Data.Rejection.Items, 1)
			rejected := source.Body.Data.Rejection.Items[0]
			require.Equal(t, body.Items[1].ItemID, rejected.ItemID)
			require.Equal(t, body.Items[1].Comment, rejected.Comment)
			require.EqualValues(t, 31, rejected.EvidenceID)
			require.EqualValues(t, 50, rejected.SourceIntakeID)
			require.Len(t, rejected.Anchors, 1)
			require.Equal(t, frozenAnchor.ThreadID, rejected.Anchors[0].ThreadID)
			require.Equal(t, frozenAnchor.AnchorExcerpt, rejected.Anchors[0].AnchorExcerpt)
			require.JSONEq(t, string(frozenAnchor.Anchor), string(rejected.Anchors[0].Anchor))
			_, e = m.getPackageReworkHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: id})
			require.Error(t, e)
			require.NoError(t, db.Table("g_intake_records").Where("kind='REWORK'").Count(&count).Error)
			require.EqualValues(t, 1, count)
			var realSource struct{ ContactID, ChannelID, OriginAcceptanceID guid.ID }
			require.NoError(t, db.Table("g_intake_records").Where("id=?", source.Body.Data.ID).Scan(&realSource).Error)
			require.EqualValues(t, 1, realSource.ContactID)
			require.EqualValues(t, 3, realSource.ChannelID)
			require.Equal(t, source.Body.Data.ReceiptID, realSource.OriginAcceptanceID)
			require.False(t, projects.changed)
		})
	}
}

func TestPackageReworkRejectsIncompleteOrInconsistentProviderFacts(t *testing.T) {
	m, db, _, _ := harness(t)
	content := PackageContent{Artifacts: []delivery.ClientArtifact{{Name: "Report", VersionID: 40, SHA256: strings.Repeat("a", 64)}}}
	ref := delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30}
	raw, _ := json.Marshal(packageManifest{Content: content, Refs: []delivery.ClientArtifactRef{ref}})
	row := &model.DeliveryPackage{ID: 1, TenantID: 1, ProjectID: 3, ContentHash: strings.Repeat("a", 64), Manifest: string(raw)}
	identity := packageItems(row, content)[0].ItemID
	valid := []PackageItemDecision{{ItemID: identity, Result: "REJECTED", FeedbackCategory: "OTHER", Comment: "A detailed rejection reason"}}
	for _, scenario := range []string{"missing-outcome", "wrong-identity", "no-rejected", "reader-failure", "incomplete-reader", "wrong-work", "writer-failure"} {
		t.Run(scenario, func(t *testing.T) {
			outcomes := append([]PackageItemDecision(nil), valid...)
			m.packageSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
				if scenario == "reader-failure" {
					return nil, ErrUnavailable
				}
				if scenario == "incomplete-reader" {
					return nil, nil
				}
				work := guid.ID(11)
				if scenario == "wrong-work" {
					work = 12
				}
				return []delivery.PackageSource{{WorkItemID: work, RequirementID: 10, IntakeID: 50}}, nil
			}
			m.reworkSources.CreateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, intake.ReworkDraft) (intake.ReworkSourceView, bool, error) {
				return intake.ReworkSourceView{}, false, ErrUnavailable
			}
			switch scenario {
			case "missing-outcome":
				outcomes = nil
			case "wrong-identity":
				outcomes[0].ItemID = "wrong"
			case "no-rejected":
				outcomes[0].Result = "ACCEPTED"
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, _, e := m.packageReworkOn(testContext(t, 2), tx, row, 2, 4, outcomes)
				return e
			}))
		})
	}
}

func TestPackageReworkUnavailableInvalidAndForeignReads(t *testing.T) {
	m, db, _, _ := harness(t)
	for _, in := range []*ObjectInput{{EntityID: "bad", ID: "1"}, {EntityID: "3", ID: "bad"}, {EntityID: "3", ID: "1"}} {
		_, e := m.getPackageReworkHTTP(testContext(t, 2), in)
		require.Error(t, e)
	}
	_, e := m.retryPackageReworkHTTP(testContext(t, 2), &EditInput[ClientworkPublishBody]{EntityID: "3", ID: "bad", IfMatch: "\"1\""})
	require.Error(t, e)
	row := &model.DeliveryPackage{ID: 1, TenantID: 1, ProjectID: 3, ContentHash: strings.Repeat("a", 64), Manifest: "{"}
	m.packageSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
		return nil, ErrUnavailable
	}
	m.reworkSources.CreateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, intake.ReworkDraft) (intake.ReworkSourceView, bool, error) {
		t.Fatal("invalid manifest cannot create source")
		return intake.ReworkSourceView{}, false, nil
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, _, e := m.packageReworkOn(testContext(t, 2), tx, row, 2, 4, nil); return e }))
	row.Manifest = `{"content":{"artifacts":[]},"refs":[]}`
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, _, e := m.packageReworkOn(testContext(t, 2), tx, row, 2, 4, nil); return e }))
}
