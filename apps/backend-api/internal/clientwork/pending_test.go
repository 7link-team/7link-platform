package clientwork

import (
	"errors"
	"slices"
	"testing"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClientPendingMergesRealDecisionTargetsAndRemovesCompleted(t *testing.T) {
	m, db, _, clients := harness(t)
	ctx := testContext(t, 4)
	sheet := draftAndPublish(t, m, db)
	changeSheet := draftAndPublish(t, m, db)
	var change, pkg Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if _, e := m.signSheetOn(ctx, tx, 3, changeSheet.ID, changeSheet.Version, SignoffBody{1, changeSheet.ContentHash, "SIGN", "Reviewed"}); e != nil {
			return e
		}
		var e error
		change, e = m.createChangeOn(ctx, tx, 3, ChangeBody{SheetID: changeSheet.ID, BaseRevision: 1, Description: "Add an export"})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		change, e = m.actChangeOn(testContext(t, 2), tx, 3, change.ID, change.Version, ChangeAction{Action: "ASSESS", Impact: "An extra milestone"})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		pkg, e = m.createPackageOn(testContext(t, 2), tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Published notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
		if e != nil {
			return e
		}
		pkg, e = publishBuiltPackage(t, m, testContext(t, 2), tx, 3, pkg.ID, pkg.Version)
		return e
	}))
	expected := []guid.ID{sheet.ID, change.ID, pkg.ID}
	slices.Sort(expected)
	var got []guid.ID
	cursor := ""
	for pageNumber := 0; pageNumber < 3; pageNumber++ {
		page, e := m.listPendingHTTP(ctx, &ListInput{EntityID: "3", Limit: 1, Cursor: cursor})
		require.NoError(t, e)
		require.Len(t, page.Body.Data.Items, 1)
		got = append(got, page.Body.Data.Items[0].ID)
		require.Equal(t, pageNumber < 2, page.Body.Data.HasMore)
		cursor = page.Body.Data.NextCursor
	}
	require.Equal(t, expected, got)
	other, e := m.listPendingHTTP(testContext(t, 5), &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, e)
	require.Empty(t, other.Body.Data.Items)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.signSheetOn(ctx, tx, 3, sheet.ID, sheet.Version, SignoffBody{1, sheet.ContentHash, "SIGN", "Reviewed"})
		return e
	}))
	page, e := m.listPendingHTTP(ctx, &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 2)
	for _, item := range page.Body.Data.Items {
		require.NotEqual(t, sheet.ID, item.ID)
	}
	clients.revoked = true
	page, e = m.listPendingHTTP(ctx, &ListInput{EntityID: "3", Limit: 20})
	require.Error(t, e)
	require.Nil(t, page)
}

func TestClientPendingNeverExposesUnpublishedTitlesOrOtherScopes(t *testing.T) {
	m, db, _, _ := harness(t)
	sheet := draftAndPublish(t, m, db)
	q := query.BindSheets(db, m.names)
	// A pending read must use the published immutable title even if a draft
	// editor changes the mutable aggregate's title.
	_, e := q.WithContext(t.Context()).Where(q.ID.Eq(sheet.ID)).Update(q.Title, "PRIVATE DRAFT")
	require.NoError(t, e)
	page, e := m.listPendingHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, "Scope", page.Body.Data.Items[0].Title)
	for _, input := range []*ListInput{{EntityID: "bad", Limit: 20}, {EntityID: "4", Limit: 20}, {EntityID: "3", Limit: 0}, {EntityID: "3", Limit: 101}, {EntityID: "3", Limit: 20, Cursor: "bad"}} {
		_, e = m.listPendingHTTP(testContext(t, 4), input)
		require.Error(t, e)
	}
	_, e = m.listPendingHTTP(testContext(t, 2), &ListInput{EntityID: "3", Limit: 20})
	require.Error(t, e, "internal users cannot impersonate the client's pending queue")
	_, e = (&Module{}).listPendingHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 20})
	require.Error(t, e)
	// A superseded published revision is not an actionable signoff target.
	_, e = q.WithContext(t.Context()).Where(q.ID.Eq(sheet.ID)).Update(q.CurrentRevision, 2)
	require.NoError(t, e)
	page, e = m.listPendingHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, e)
	require.Empty(t, page.Body.Data.Items)
	require.True(t, externalRouteAllowed("GET", []string{"entities", "3", "client-pending"}, 4))
	require.False(t, externalRouteAllowed("POST", []string{"entities", "3", "client-pending"}, 4))
	require.False(t, externalRouteAllowed("GET", []string{"entities", "3", "client-pending", "1"}, 4))
}

func TestClientPendingQueryFailuresAreNotSuccessfulEmptyQueues(t *testing.T) {
	for _, table := range []string{"sheets", "sheet_revisions", "change_requests", "packages"} {
		t.Run(table, func(t *testing.T) {
			m, db, _, _ := harness(t)
			draftAndPublish(t, m, db)
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("pending_query_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == "g_clientwork_"+table {
					_ = tx.AddError(errors.New("query unavailable"))
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("pending_query_failure")) })
			out, e := m.listPendingHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 20})
			require.Error(t, e)
			require.Nil(t, out)
		})
	}
}
