package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestProjectStageConnectorPersistsExactCancellationDecisionAndRollsBack(t *testing.T) {
	m, db, _, _ := harness(t)
	missing := module.NewServices()
	require.NoError(t, missing.Seal())
	require.NoError(t, m.bindProjectStages(missing))
	invalid := module.NewServices()
	require.NoError(t, module.Provide[delivery.ProjectStagePackagesConnector](invalid, delivery.ProjectStagePackagesConnector{}))
	require.NoError(t, invalid.Seal())
	require.ErrorIs(t, m.bindProjectStages(invalid), ErrUnavailable)
	var provider delivery.ProjectStagePackages
	services := module.NewServices()
	require.NoError(t, module.Provide[delivery.ProjectStagePackagesConnector](services, delivery.ProjectStagePackagesConnector{Bind: func(p delivery.ProjectStagePackages) error { provider = p; return nil }}))
	require.NoError(t, services.Seal())
	require.NoError(t, m.bindProjectStages(services))
	require.NotNil(t, provider.FactsOn)
	require.NotNil(t, provider.ArchiveOn)
	require.NotNil(t, provider.DecisionOn)
	ctx := testContext(t, 2)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, provider.DecisionOn(ctx, tx, 1, 3, 900, 7, "Cancellation verified from independent evidence", "IAM:project_cancel"))
		return ErrConflict
	}), ErrConflict)
	var count int64
	require.NoError(t, db.Table(m.names.CommandDecision.String()).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return provider.DecisionOn(ctx, tx, 1, 3, 900, 7, "Cancellation verified from independent evidence", "IAM:project_cancel")
	}))
	var row model.CommandDecision
	require.NoError(t, db.Table(m.names.CommandDecision.String()).Take(&row).Error)
	require.Equal(t, "PROJECT", row.TargetKind)
	require.Equal(t, "CANCEL", row.Action)
	require.EqualValues(t, 2, row.ActorID)
	require.EqualValues(t, 3, row.TargetID)
	require.EqualValues(t, 3, row.SourceID)
	require.Equal(t, 7, row.SourceVersion)
	require.Equal(t, "IAM:project_cancel", row.AuthoritySource)
	_, err := provider.FactsOn(ctx, db, 1, 3)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = provider.FactsOn(ctx, nil, 1, 3)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestProjectStageSourceFailuresCannotBecomeCompletionFacts(t *testing.T) {
	for _, fault := range []string{"receipt_read", "duplicate_receipt", "wrong_hash", "manifest", "content", "refs", "outcomes", "missing_item", "invalid_result", "dispositions", "rework_capability", "rework_failure", "open_rework", "declined"} {
		t.Run(fault, func(t *testing.T) {
			m, db, pkg := releaseNoteHarness(t)
			ctx := testContext(t, 2)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := publishBuiltPackage(t, m, ctx, tx, 3, pkg, 1); return e }))
			var pack model.DeliveryPackage
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&pack).Error)
			var manifest packageManifest
			require.NoError(t, json.Unmarshal([]byte(pack.Manifest), &manifest))
			item := packageItems(&pack, manifest.Content)[0].ItemID
			receipt := model.PackageReceipt{TenantID: 1, ID: 900, ProjectID: 3, PackageID: pkg, Decision: "REJECT", ContentHash: pack.ContentHash, CreatedAt: now()}
			require.NoError(t, db.Table(m.names.PackageReceipt.String()).Create(&receipt).Error)
			switch fault {
			case "receipt_read", "dispositions":
				target := m.names.PackageReceipt.String()
				if fault == "dispositions" {
					target = m.names.ReworkDisposition.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("stage_source_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == target {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "duplicate_receipt":
				receipt.ID++
				require.Error(t, db.Table(m.names.PackageReceipt.String()).Create(&receipt).Error, "one exact receipt per package is enforced by storage")
				return
			case "wrong_hash":
				require.NoError(t, db.Table(m.names.PackageReceipt.String()).Where("id=900").Update("content_hash", "different").Error)
			case "manifest":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("manifest", "[]").Error)
			case "content", "refs":
				if fault == "content" {
					manifest.Content.Title = "Tampered"
				} else {
					manifest.Refs = nil
				}
				raw, e := json.Marshal(manifest)
				require.NoError(t, e)
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("manifest", string(raw)).Error)
			case "outcomes", "missing_item", "invalid_result":
				text := "{"
				if fault == "missing_item" {
					text = `[]`
				}
				if fault == "invalid_result" {
					raw, e := json.Marshal([]PackageItemDecision{{ItemID: item, Result: "MAYBE"}})
					require.NoError(t, e)
					text = string(raw)
				}
				require.NoError(t, db.Table(m.names.PackageReceipt.String()).Where("id=900").Update("item_outcomes", text).Error)
			case "rework_capability":
				m.reworkSources.ReadOn = nil
			case "rework_failure", "open_rework", "declined":
				m.reworkSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.ReworkSourceView, bool, error) {
					if fault == "rework_failure" {
						return intake.ReworkSourceView{}, false, ErrUnavailable
					}
					if fault == "declined" {
						return intake.ReworkSourceView{State: "DECLINED"}, true, nil
					}
					return intake.ReworkSourceView{}, false, nil
				}
			}
			var facts delivery.ProjectStagePackageFacts
			err := db.Transaction(func(tx *gorm.DB) error { var e error; facts, e = m.projectStageFactsOn(ctx, tx, 1, 3); return e })
			if fault == "open_rework" || fault == "declined" {
				require.NoError(t, err)
				require.Equal(t, fault == "open_rework", facts.OpenRework)
				require.False(t, facts.HasAccepted)
				require.Len(t, facts.Outcomes, 1)
				require.False(t, facts.Outcomes[0].Accepted)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestProjectStagePackagesUsesActualReceiptAndDraftArchiveRollback(t *testing.T) {
	m, db, _, _ := harness(t)
	create := func(key string) Receipt {
		created, e := m.create_packageHTTP(testContext(t, 2), &CreateInput[PackageBody]{EntityID: "3", Key: key, Body: PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
		require.NoError(t, e)
		return created.Body.Data
	}
	published := create("published")
	draft := create("draft")
	_, e := publishBuiltPackageHTTP(t, m, testContext(t, 2), &EditInput[PackagePublishBody]{Body: PackagePublishBody{ReauthProof: "test-publish-proof"}, EntityID: "3", ID: published.ID.String(), Key: "publish", IfMatch: `"1"`})
	require.NoError(t, e)
	read := func() delivery.ProjectStagePackageFacts {
		var out delivery.ProjectStagePackageFacts
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.projectStageFactsOn(t.Context(), tx, 1, 3); return e }))
		return out
	}
	facts := read()
	require.True(t, facts.Published)
	require.True(t, facts.Pending)
	require.False(t, facts.HasAccepted)
	view, e := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: published.ID.String()})
	require.NoError(t, e)
	_, e = m.package_acceptanceHTTP(testContext(t, 4), &EditInput[PackageDecision]{EntityID: "3", ID: published.ID.String(), Key: "receipt", IfMatch: `"2"`, Body: PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: view.Body.Data.ContentHash, Decision: "ACCEPT", Reason: "Reviewed actual output"}})
	require.NoError(t, e)
	facts = read()
	require.False(t, facts.Pending)
	require.True(t, facts.HasAccepted)
	require.Len(t, facts.Outcomes, 1)
	require.EqualValues(t, 20, facts.Outcomes[0].ResultID)
	require.True(t, facts.Outcomes[0].Accepted)
	require.False(t, facts.Outcomes[0].DecidedAt.IsZero())
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.archiveProjectDraftPackagesOn(testContext(t, 2), tx, 1, 3))
		return ErrConflict
	}))
	q := query.BindDeliveryPackage(db, m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(draft.ID)).First()
	require.NoError(t, e)
	require.Nil(t, row.ArchivedAt)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.archiveProjectDraftPackagesOn(testContext(t, 2), tx, 1, 3) }))
	row, e = q.WithContext(t.Context()).Where(q.ID.Eq(draft.ID)).First()
	require.NoError(t, e)
	require.NotNil(t, row.ArchivedAt)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		other, e := m.projectStageFactsOn(t.Context(), tx, 2, 3)
		require.NoError(t, e)
		require.False(t, other.Published)
		return nil
	}))
}
