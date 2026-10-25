package clientwork

import (
	"testing"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPackageTimelineRetainsObservedTransitionsAndCurrentAudience(t *testing.T) {
	m, db, _, clients := harness(t)
	ctx := testContext(t, 2)
	created, err := m.create_packageHTTP(ctx, &CreateInput[PackageBody]{EntityID: "3", Key: "timeline-draft", Body: PackageBody{Title: "Release", ReleaseNotes: "Observed content", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
	require.NoError(t, err)
	id := created.Body.Data.ID
	input := &PackageTimelineInput{ListInput: ListInput{EntityID: "3", Limit: 1}, ID: id.String()}
	page, err := m.packageTimelineHTTP(ctx, input)
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, "DRAFT", page.Body.Data.Items[0].State)
	_, err = m.packageTimelineHTTP(testContext(t, 4), input)
	require.Error(t, err, "private draft history is not client-visible")
	_, err = publishBuiltPackageHTTP(t, m, ctx, &EditInput[PackagePublishBody]{Body: PackagePublishBody{ReauthProof: "test-publish-proof"}, EntityID: "3", ID: id.String(), Key: "timeline-publish", IfMatch: "\"1\""})
	require.NoError(t, err)
	page, err = m.packageTimelineHTTP(ctx, input)
	require.NoError(t, err)
	require.True(t, page.Body.Data.HasMore)
	input.Cursor = page.Body.Data.NextCursor
	next, err := m.packageTimelineHTTP(ctx, input)
	require.NoError(t, err)
	require.Len(t, next.Body.Data.Items, 1)
	require.Equal(t, "PUBLISHED", next.Body.Data.Items[0].State)
	require.NotEqual(t, page.Body.Data.Items[0].ID, next.Body.Data.Items[0].ID)
	input.Cursor = ""
	page, err = m.packageTimelineHTTP(testContext(t, 4), input)
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, "PUBLISHED", page.Body.Data.Items[0].State)
	_, err = m.packageTimelineHTTP(testContext(t, 5), input)
	require.Error(t, err)
	clients.revoked = true
	_, err = m.packageTimelineHTTP(testContext(t, 4), input)
	require.Error(t, err)
	q := query.BindPackageTimeline(db, m.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count, "reads cannot invent history")
}

func TestPackageTimelineInvalidAndFailedRecordsLeaveNoHistory(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	for _, input := range []*PackageTimelineInput{{ID: "bad"}, {ID: "999", ListInput: ListInput{EntityID: "3", Limit: 20}}, {ID: "1", ListInput: ListInput{EntityID: "bad", Limit: 20}}} {
		_, err := m.packageTimelineHTTP(ctx, input)
		require.Error(t, err)
	}
	var receipt Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		receipt, err = m.createPackageOn(ctx, tx, 3, PackageBody{Title: "Release", ReleaseNotes: "No invented history", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
		return err
	}))
	for _, scenario := range []string{"version", "missing", "id"} {
		id, version := receipt.ID, receipt.Version
		if scenario == "version" {
			version++
		}
		if scenario == "missing" {
			id = 999
		}
		if scenario == "id" {
			m.ids = &unavailableIDs{source: m.ids, at: 1}
		}
		err := db.Transaction(func(tx *gorm.DB) error { return m.recordPackageTimelineOn(ctx, tx, 3, id, "package_drafted", version) })
		require.Error(t, err)
	}
	q := query.BindPackageTimeline(db, m.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}
