package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestProjectMetricClientRejectsCorruptSourcesAndMarksIncompleteItems(t *testing.T) {
	m, db, pkg := releaseNoteHarness(t)
	ctx := testContext(t, 2)
	var pack model.DeliveryPackage
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&pack).Error)
	var manifest packageManifest
	require.NoError(t, json.Unmarshal([]byte(pack.Manifest), &manifest))
	item := packageItems(&pack, manifest.Content)[0].ItemID
	valid := `[{"item_id":"` + item + `","result":"ACCEPTED"}]`
	receipt := model.PackageReceipt{TenantID: 1, ID: 900, ProjectID: 3, PackageID: pkg, ContentHash: pack.ContentHash, Decision: "ACCEPT", ItemOutcomes: &valid, CreatedAt: now()}
	require.NoError(t, db.Table(m.names.PackageReceipt.String()).Create(&receipt).Error)
	for _, fault := range []string{"receipt_read", "package_read", "manifest", "outcomes", "count", "unknown"} {
		t.Run(fault, func(t *testing.T) {
			if fault == "receipt_read" || fault == "package_read" {
				target := m.names.PackageReceipt.String()
				if fault == "package_read" {
					target = m.names.DeliveryPackage.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("client_metric_source_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == target {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			if fault == "manifest" {
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("manifest", "{").Error)
			}
			if fault == "outcomes" || fault == "count" || fault == "unknown" {
				text := "{"
				if fault == "count" {
					text = "[]"
				}
				if fault == "unknown" {
					text = `[{"item_id":"` + strings.Repeat("b", 64) + `","result":"ACCEPTED"}]`
				}
				require.NoError(t, db.Table(m.names.PackageReceipt.String()).Where("id=900").Update("item_outcomes", text).Error)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				out, e := m.metricClientOn(ctx, tx, 1, 3, []guid.ID{11}, now().Add(time.Hour))
				if fault == "count" || fault == "unknown" {
					require.NoError(t, e)
					require.False(t, out.Complete)
				}
				return e
			})
			if fault != "count" && fault != "unknown" {
				require.Error(t, err)
			}
			require.NoError(t, db.Callback().Query().Remove("client_metric_source_failure"))
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("manifest", pack.Manifest).Error)
			require.NoError(t, db.Table(m.names.PackageReceipt.String()).Where("id=900").Update("item_outcomes", valid).Error)
		})
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.metricClientOn(ctx, tx, 1, 3, nil, now())
		require.Empty(t, out.Receipts)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.metricClientOn(ctx, tx, 1, 3, []guid.ID{0}, now()); return e }), ErrInvalid)
	_, err := m.metricClientOn(ctx, db, 1, 3, []guid.ID{11}, now())
	require.ErrorIs(t, err, ErrInvalid)
	_, err = m.metricClientOn(t.Context(), db, 1, 3, []guid.ID{11}, now())
	require.ErrorIs(t, err, ErrForbidden)
	s := module.NewServices()
	require.NoError(t, module.Provide[delivery.DeliveryMetricsSourcesConnector](s, delivery.DeliveryMetricsSourcesConnector{}))
	require.NoError(t, s.Seal())
	require.ErrorIs(t, m.bindMetrics(s), ErrUnavailable)
}

func TestProjectMetricsClientUsesFrozenItemOutcomesAndCurrentAuthority(t *testing.T) {
	m, db, _, _ := harness(t)
	var pkg Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		pkg, e = m.createPackageOn(testContext(t, 2), tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Frozen manifest", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
		if e != nil {
			return e
		}
		pkg, e = publishBuiltPackage(t, m, testContext(t, 2), tx, 3, pkg.ID, pkg.Version)
		return e
	}))
	view, err := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: pkg.ID.String()})
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.decidePackageOn(testContext(t, 4), tx, 3, pkg.ID, pkg.Version, PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: view.Body.Data.ContentHash, Reason: "Actual rejection", Items: []PackageItemDecision{{ItemID: view.Body.Data.Items[0].ItemID, Result: "REJECTED", FeedbackCategory: "FUNCTIONAL_DEFECT", Comment: "Does not satisfy the frozen requirement"}}})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.metricClientOn(testContext(t, 2), tx, 1, 3, []guid.ID{11}, time.Now().Add(time.Hour))
		require.NoError(t, e)
		require.True(t, out.Complete)
		require.Len(t, out.Receipts, 2)
		require.True(t, out.Receipts[0].Package)
		require.False(t, out.Receipts[0].Accepted)
		require.EqualValues(t, 11, out.Receipts[1].WorkItemID)
		require.NotEmpty(t, out.Receipts[1].ItemKey)
		hidden, e := m.metricClientOn(testContext(t, 2), tx, 1, 3, []guid.ID{99}, time.Now().Add(time.Hour))
		require.NoError(t, e)
		require.Empty(t, hidden.Receipts)
		_, e = m.metricClientOn(testContext(t, 4), tx, 1, 3, []guid.ID{11}, time.Now().Add(time.Hour))
		require.Error(t, e, "external identities cannot read private metrics")
		return nil
	}))
	q := query.BindPackageReceipt(db, m.names)
	_, err = q.WithContext(t.Context()).Where(q.PackageID.Eq(pkg.ID)).Update(q.ItemOutcomes, nil)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.metricClientOn(testContext(t, 2), tx, 1, 3, []guid.ID{11}, time.Now().Add(time.Hour))
		require.False(t, out.Complete)
		require.Empty(t, out.Receipts)
		return e
	}))
}
