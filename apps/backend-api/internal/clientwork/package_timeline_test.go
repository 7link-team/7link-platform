package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestPackageTimelineRecordsExactVersionsAndHidesPrivateDraftHistory(t *testing.T) {
	m, db, _, clients := harness(t)
	ctx := testContext(t, 2)
	body := PackageBody{Title: "Original", ReleaseNotes: "Original notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}
	var pkg Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; pkg, e = m.createPackageOn(ctx, tx, 3, body); return e }))
	in := &PackageTimelineInput{ListInput: ListInput{EntityID: "3", Limit: 1}, ID: pkg.ID.String()}
	body.Title = "Revised"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; pkg, e = m.editPackageOn(ctx, tx, 3, pkg.ID, 1, body); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		pkg, e = publishBuiltPackage(t, m, ctx, tx, 3, pkg.ID, 2)
		return e
	}))
	first, e := m.packageTimelineHTTP(ctx, in)
	require.NoError(t, e)
	require.Len(t, first.Body.Data.Items, 1)
	require.Equal(t, 1, first.Body.Data.Items[0].Version)
	require.True(t, first.Body.Data.HasMore)
	in.Cursor = first.Body.Data.NextCursor
	second, e := m.packageTimelineHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, 2, second.Body.Data.Items[0].Version)
	require.NotEqual(t, first.Body.Data.Items[0].ContentHash, second.Body.Data.Items[0].ContentHash)
	in.Cursor = ""
	in.Limit = 20
	external, e := m.packageTimelineHTTP(testContext(t, 4), in)
	require.NoError(t, e)
	require.Len(t, external.Body.Data.Items, 1)
	require.Equal(t, "PUBLISHED", external.Body.Data.Items[0].State)
	view, e := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: pkg.ID.String()})
	require.NoError(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.decidePackageOn(testContext(t, 4), tx, 3, pkg.ID, pkg.Version, PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: view.Body.Data.ContentHash, Decision: "ACCEPT", Reason: "Accepted fixed manifest"})
		return e
	}))
	external, e = m.packageTimelineHTTP(testContext(t, 4), in)
	require.NoError(t, e)
	require.Len(t, external.Body.Data.Items, 2)
	require.Equal(t, "ACCEPTED", external.Body.Data.Items[1].State)
	_, e = m.packageTimelineHTTP(testContext(t, 5), in)
	require.Error(t, e)
	clients.revoked = true
	_, e = m.packageTimelineHTTP(testContext(t, 4), in)
	require.Error(t, e)
}
func TestPackageTimelineFailureRollsBackPackageAuditAndOutbox(t *testing.T) {
	m, db, _, _ := harness(t)
	failure := errors.New("history write failed")
	fired := false
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("timeline_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.PackageTimeline.String() {
			fired = true
			_ = tx.AddError(failure)
		}
	}))
	e := db.Transaction(func(tx *gorm.DB) error {
		_, e := m.createPackageOn(testContext(t, 2), tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
		return e
	})
	require.True(t, fired)
	require.ErrorIs(t, e, failure)
	n, e := query.BindDeliveryPackage(db, m.names).WithContext(testContext(t, 2)).Count()
	require.NoError(t, e)
	require.Zero(t, n)
	var auditCount int64
	require.NoError(t, db.Table("audit_probe").Count(&auditCount).Error)
	require.Zero(t, auditCount)
}
