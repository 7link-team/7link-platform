package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPackageCommentAuthorityUsesFrozenItemAndCurrentRecipient(t *testing.T) {
	m, db, _, clients := harness(t)
	ctx := testContext(t, 4)
	created, err := m.create_packageHTTP(testContext(t, 2), &CreateInput[PackageBody]{EntityID: "3", Key: "comment-package", Body: PackageBody{Title: "Review", ReleaseNotes: "Actual report", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
	require.NoError(t, err)
	id := created.Body.Data.ID
	view, err := m.get_packageHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: id.String()})
	require.NoError(t, err)
	item := view.Body.Data.Items[0].ItemID
	read := func(actor, tenant guid.ID, write bool) (execution.ClientDeliveryItem, error) {
		var out execution.ClientDeliveryItem
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.clientCommentItemOn(testContext(t, actor), tx, tenant, 3, id, item, write)
			return e
		})
		return out, err
	}
	_, err = read(4, 1, false)
	require.ErrorIs(t, err, ErrNotFound, "unpublished content stays hidden")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := publishBuiltPackage(t, m, testContext(t, 2), tx, 3, id, 1); return e }))
	fact, err := read(4, 1, true)
	require.NoError(t, err)
	require.EqualValues(t, 11, fact.WorkItemID)
	require.EqualValues(t, 30, fact.EvidenceID)
	require.EqualValues(t, 40, fact.VersionID)
	require.EqualValues(t, 4, fact.ContactID)
	require.Equal(t, strings.Repeat("a", 64), fact.SHA256)
	open := func() (files.PinnedResourceRead, error) {
		var out files.PinnedResourceRead
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.openClientCommentItemOn(ctx, tx, 1, 3, id, item)
			return e
		})
		return out, err
	}
	opened, err := open()
	require.NoError(t, err)
	raw, err := io.ReadAll(opened.Reader)
	require.NoError(t, err)
	require.Equal(t, "test", string(raw))
	require.NoError(t, opened.Reader.Close())
	_, err = read(2, 1, true)
	require.ErrorIs(t, err, ErrForbidden)
	_, err = read(2, 1, false)
	require.NoError(t, err)
	_, err = read(4, 9, false)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = read(5, 1, false)
	require.ErrorIs(t, err, ErrNotFound)
	err = db.Transaction(func(tx *gorm.DB) error {
		_, e := m.clientCommentItemOn(ctx, tx, 1, 3, id, strings.Repeat("b", 64), false)
		return e
	})
	require.ErrorIs(t, err, ErrNotFound)
	contacts := m.packageCommentContacts
	m.packageCommentContacts.ContactOn = nil
	_, err = read(4, 1, true)
	require.ErrorIs(t, err, ErrUnavailable)
	m.packageCommentContacts.ContactOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error) { return 0, ErrForbidden }
	_, err = read(4, 1, true)
	require.ErrorIs(t, err, ErrForbidden)
	m.packageCommentContacts = contacts
	opener := m.files.OpenOn
	m.files.OpenOn = nil
	_, err = open()
	require.ErrorIs(t, err, ErrUnavailable)
	m.files.OpenOn = func(context.Context, *gorm.DB, guid.ID) (files.PinnedResourceRead, error) {
		return files.PinnedResourceRead{}, ErrUnavailable
	}
	_, err = open()
	require.ErrorIs(t, err, ErrUnavailable)
	m.files.OpenOn = opener
	var row model.DeliveryPackage
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", id).Take(&row).Error)
	var manifest packageManifest
	require.NoError(t, json.Unmarshal([]byte(row.Manifest), &manifest))
	manifest.Bindings = nil
	encoded, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", id).Update("manifest", string(encoded)).Error)
	_, err = open()
	require.ErrorIs(t, err, ErrNotFound)
	manifest.Refs = nil
	encoded, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", id).Update("manifest", string(encoded)).Error)
	_, err = read(4, 1, false)
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", id).Update("manifest", "[]").Error)
	_, err = read(4, 1, false)
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", id).Update("state", "ARCHIVED").Error)
	_, err = read(4, 1, true)
	require.ErrorIs(t, err, ErrConflict)
	clients.revoked = true
	_, err = read(4, 1, false)
	require.Error(t, err)
}

func TestPackageCommentCommandsReplayAndRollbackProviderWrites(t *testing.T) {
	m, db, _, clients := harness(t)
	ctx := testContext(t, 4)
	item := strings.Repeat("a", 64)
	calls := 0
	m.packageComments.CreateOn = func(_ context.Context, tx *gorm.DB, tenant, project, pkg guid.ID, hash string, body execution.ClientThreadCreate) (execution.ClientThreadView, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 3, project)
		require.EqualValues(t, 80, pkg)
		require.Equal(t, item, hash)
		require.Equal(t, "Review frozen evidence", body.Body)
		calls++
		return execution.ClientThreadView{ClientThreadAnchor: execution.ClientThreadAnchor{ThreadID: 90}, Body: body.Body}, tx.Exec("INSERT INTO baseline_probe VALUES (90)").Error
	}
	in := &packageCommentCreateInput{EntityID: "3", ID: "80", ItemHash: item, Key: "comment", Body: execution.ClientThreadCreate{Body: "Review frozen evidence"}}
	publisher := m.events
	m.events = failPublisher{}
	_, err := m.createPackageCommentHTTP(ctx, in)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
	require.Zero(t, count)
	m.events = publisher
	out, err := m.createPackageCommentHTTP(ctx, in)
	require.NoError(t, err)
	require.EqualValues(t, 90, out.Body.Data.ID)
	require.Equal(t, "OPEN", out.Body.Data.State)
	replay, err := m.createPackageCommentHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, 2, calls)
	require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	clients.revoked = true
	_, err = m.createPackageCommentHTTP(ctx, in)
	require.Error(t, err, "replay checks current client membership")
	clients.revoked = false
	for _, bad := range []packageCommentCreateInput{{EntityID: "bad", ID: "80"}, {EntityID: "3", ID: "bad"}} {
		_, err = m.createPackageCommentHTTP(ctx, &bad)
		require.Error(t, err)
	}
	in.Key = "unavailable"
	m.packageComments.CreateOn = nil
	_, err = m.createPackageCommentHTTP(ctx, in)
	require.Error(t, err)
	m.packageComments.CreateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, execution.ClientThreadCreate) (execution.ClientThreadView, error) {
		return execution.ClientThreadView{}, ErrConflict
	}
	_, err = m.createPackageCommentHTTP(ctx, in)
	require.Error(t, err)
	list := &packageCommentListInput{ListInput: ListInput{EntityID: "3", Limit: 1}, ID: "80", ItemHash: item}
	m.packageComments.ListOn = func(_ context.Context, tx *gorm.DB, tenant, project, pkg guid.ID, hash string, last guid.ID, limit int) ([]execution.ClientThreadView, error) {
		require.NotNil(t, tx)
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 3, project)
		require.EqualValues(t, 80, pkg)
		require.Equal(t, item, hash)
		require.Equal(t, 2, limit)
		if last == 0 {
			return []execution.ClientThreadView{{ClientThreadAnchor: execution.ClientThreadAnchor{ThreadID: 90}}, {ClientThreadAnchor: execution.ClientThreadAnchor{ThreadID: 89}}}, nil
		}
		require.EqualValues(t, 90, last)
		return []execution.ClientThreadView{{ClientThreadAnchor: execution.ClientThreadAnchor{ThreadID: 89}}}, nil
	}
	page, err := m.packageCommentsHTTP(ctx, list)
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 1)
	require.True(t, page.Body.Data.HasMore)
	list.Cursor = page.Body.Data.NextCursor
	page, err = m.packageCommentsHTTP(ctx, list)
	require.NoError(t, err)
	require.EqualValues(t, 89, page.Body.Data.Items[0].ThreadID)
	require.False(t, page.Body.Data.HasMore)
	list.Cursor = ""
	m.packageComments.ListOn = nil
	_, err = m.packageCommentsHTTP(ctx, list)
	require.Error(t, err)
	m.packageComments.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, guid.ID, int) ([]execution.ClientThreadView, error) {
		return nil, ErrUnavailable
	}
	_, err = m.packageCommentsHTTP(ctx, list)
	require.Error(t, err)
	list.ID = "bad"
	_, err = m.packageCommentsHTTP(ctx, list)
	require.Error(t, err)
}

func TestPackageRefusalAnchorsFreezeIDsDeduplicateAndPreserveSignature(t *testing.T) {
	m, db, _, _ := harness(t)
	row := &model.DeliveryPackage{TenantID: 1, ProjectID: 3, ID: 80}
	anchor := execution.ClientThreadAnchor{ThreadID: 90, AnchorKind: "CODE_LINES", AnchorExcerpt: "Original rejected line", VersionRef: "file-version:30", ArtifactRef: delivery.ArtifactAnchorSnapshot{Kind: "CODE_LINES", VersionID: 30, Excerpt: "Original rejected line"}}
	calls := 0
	m.packageComments.ConvertOn = func(_ context.Context, tx *gorm.DB, tenant, project, pkg guid.ID, item string, thread, target, contact guid.ID) (execution.ClientThreadAnchor, error) {
		require.NotNil(t, tx)
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 3, project)
		require.EqualValues(t, 80, pkg)
		require.Equal(t, strings.Repeat("a", 64), item)
		require.EqualValues(t, 90, thread)
		require.Positive(t, target)
		require.EqualValues(t, 4, contact)
		calls++
		return anchor, nil
	}
	outcomes := []PackageItemDecision{{ItemID: strings.Repeat("a", 64), Result: "REJECTED", FeedbackCategory: "OTHER", Comment: "A detailed rejection reason", CommentThreadIDs: []guid.ID{90, 90}}}
	at := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	before, err := packageReceiptSignature(80, 4, strings.Repeat("b", 64), "Client", "Reviewed", outcomes, at)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.freezeRejectionAnchorsOn(testContext(t, 4), tx, row, 4, outcomes) }))
	require.Equal(t, 1, calls)
	require.Equal(t, []guid.ID{90}, outcomes[0].CommentThreadIDs)
	require.Positive(t, outcomes[0].AcceptanceItemID)
	require.Equal(t, []execution.ClientThreadAnchor{anchor}, outcomes[0].Anchors)
	after, err := packageReceiptSignature(80, 4, strings.Repeat("b", 64), "Client", "Reviewed", outcomes, at)
	require.NoError(t, err)
	require.Equal(t, before.ReceiptSHA256, after.ReceiptSHA256)
	require.Equal(t, before.ReceiptJSON, after.ReceiptJSON)
	raw, err := json.Marshal(outcomes)
	require.NoError(t, err)
	var restored []PackageItemDecision
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.Equal(t, outcomes, restored)
}

func TestPackageRefusalAnchorsRejectInvalidReferencesAndUntrustedSnapshots(t *testing.T) {
	m, db, _, _ := harness(t)
	row := &model.DeliveryPackage{TenantID: 1, ProjectID: 3, ID: 80}
	for name, item := range map[string]PackageItemDecision{
		"accepted":           {Result: "ACCEPTED", CommentThreadIDs: []guid.ID{90}},
		"too_many":           {Result: "REJECTED", CommentThreadIDs: make([]guid.ID, 21)},
		"untrusted_snapshot": {Result: "REJECTED", Anchors: []execution.ClientThreadAnchor{{ThreadID: 90}}},
		"untrusted_target":   {Result: "REJECTED", AcceptanceItemID: 90},
		"invalid_id":         {Result: "REJECTED", CommentThreadIDs: []guid.ID{0}},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				return m.freezeRejectionAnchorsOn(testContext(t, 4), tx, row, 4, []PackageItemDecision{item})
			}))
		})
	}
	for _, reason := range []string{"NOT_THIS_ITEM", "NOT_CLIENT_THREAD", "ALREADY_CONVERTED"} {
		t.Run(reason, func(t *testing.T) {
			m.packageComments.ConvertOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, guid.ID, guid.ID, guid.ID) (execution.ClientThreadAnchor, error) {
				return execution.ClientThreadAnchor{}, &execution.ClientThreadError{Reason: reason}
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				return m.freezeRejectionAnchorsOn(testContext(t, 4), tx, row, 4, []PackageItemDecision{{Result: "REJECTED", CommentThreadIDs: []guid.ID{90}}})
			})
			var refusal *execution.ClientThreadError
			require.ErrorAs(t, err, &refusal)
			require.Equal(t, reason, refusal.Reason)
		})
	}
	m.packageComments.ConvertOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, guid.ID, guid.ID, guid.ID) (execution.ClientThreadAnchor, error) {
		return execution.ClientThreadAnchor{}, errors.New("source offline")
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		return m.freezeRejectionAnchorsOn(testContext(t, 4), tx, row, 4, []PackageItemDecision{{Result: "REJECTED", CommentThreadIDs: []guid.ID{90}}})
	}))
}
