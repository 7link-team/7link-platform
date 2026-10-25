package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPublicationConfirmationRejectsUnverifiableDrafts(t *testing.T) {
	for _, stage := range []string{"clients_missing", "client_read", "revoked", "projects_missing", "manifest", "no_pins", "invalid_pin", "missing_version", "artifacts_changed", "read"} {
		t.Run(stage, func(t *testing.T) {
			m, db, _, clients := harness(t)
			ctx := testContext(t, 2)
			created, err := m.create_packageHTTP(ctx, &CreateInput[PackageBody]{EntityID: "3", Key: "draft-facts", Body: PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
			require.NoError(t, err)
			id := created.Body.Data.ID
			var raw string
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", id).Select("manifest").Scan(&raw).Error)
			var manifest packageManifest
			require.NoError(t, json.Unmarshal([]byte(raw), &manifest))
			switch stage {
			case "clients_missing":
				m.clients = nil
			case "client_read":
				clients.clientErr = errors.New("client directory unavailable")
			case "revoked":
				clients.revoked = true
			case "projects_missing":
				m.projects = nil
			case "manifest":
				raw = "{"
			case "no_pins":
				manifest.Bindings = nil
			case "invalid_pin":
				manifest.Bindings[0].BindingID = 0
			case "missing_version":
				manifest.Bindings[0].VersionID = 999
			case "artifacts_changed":
				manifest.Content.Artifacts[0].Name = "different bytes"
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_confirmation", func(tx *gorm.DB) { tx.AddError(errors.New("publication read unavailable")) }))
			}
			if stage != "manifest" {
				encoded, e := json.Marshal(manifest)
				require.NoError(t, e)
				raw = string(encoded)
			}
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", id).Update("manifest", raw).Error)
			err = db.Transaction(func(tx *gorm.DB) error {
				_, found, e := m.packageConfirmationSourceOn(ctx, tx, 1, 3, id, true)
				require.False(t, found, "unverifiable draft must not become confirmation authority")
				if stage == "revoked" {
					require.NoError(t, e)
				} else {
					require.Error(t, e)
				}
				return nil
			})
			require.NoError(t, err)
			require.NoError(t, db.Callback().Query().Remove("regression_confirmation"))
		})
	}
}

func TestFactReadersRequireTransactionsAndPropagateCorruption(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	ctx := testContext(t, 2)
	for _, tx := range []*gorm.DB{nil, db} {
		_, found, err := m.packageConfirmationSourceOn(ctx, tx, 1, 3, 1, true)
		require.False(t, found)
		require.ErrorIs(t, err, delivery.ErrInvalidArgument)
		_, found, err = m.clarificationReplySourceOn(ctx, tx, 1, 1, true)
		require.False(t, found)
		require.ErrorIs(t, err, ErrInvalid)
		_, found, err = m.channelDispatchInitiatorOn(ctx, tx, 1, 1, nil)
		require.False(t, found)
		require.ErrorIs(t, err, ErrInvalid)
	}
	opened, err := m.requestClarificationHTTP(ctx, clarificationRequest())
	require.NoError(t, err)
	require.NoError(t, db.Table(m.names.ClarificationRound.String()).Where("id=?", opened.Body.Data.ID).Update("questions", "{").Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, err := m.clarificationReplySourceOn(ctx, tx, 1, opened.Body.Data.ID, true)
		require.False(t, found)
		require.Error(t, err)
		return nil
	}))
	failure := errors.New("fact store unavailable")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_fact", func(tx *gorm.DB) { tx.AddError(failure) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, err := m.clarificationReplySourceOn(ctx, tx, 1, 1, false)
		require.False(t, found)
		require.ErrorIs(t, err, failure)
		_, found, err = m.channelDispatchInitiatorOn(ctx, tx, 1, 1, func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID, string) (guid.ID, bool, error) {
			return 0, false, nil
		})
		require.False(t, found)
		require.ErrorIs(t, err, failure)
		return nil
	}))
}

func TestConfirmationUsesActualDraftPublicationFacts(t *testing.T) {
	m, db, projects, _ := harness(t)
	ctx := testContext(t, 2)
	body := PackageBody{Title: "Ready release", ReleaseNotes: "Actual release notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}
	created, err := m.create_packageHTTP(ctx, &CreateInput[PackageBody]{EntityID: "3", Key: "confirmation-source", Body: body})
	require.NoError(t, err)
	id := created.Body.Data.ID
	call := func(lock bool) (delivery.ClientPackageConfirmationFact, bool, error) {
		var f delivery.ClientPackageConfirmationFact
		var found bool
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			f, found, err = m.packageConfirmationSourceOn(ctx, tx, 1, 3, id, lock)
			return err
		})
		return f, found, err
	}
	f, found, err := call(true)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, f.Draft)
	require.Equal(t, 1, f.Version)
	require.Equal(t, id, f.PackageID)
	projects.stale = true
	_, _, err = call(true)
	require.Error(t, err)
	projects.stale = false
	q := query.BindDeliveryPackage(db, m.names)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, err := m.packageConfirmationSourceOn(ctx, tx, 9, 3, id, false)
		require.NoError(t, err)
		require.False(t, found)
		return nil
	}))
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", id).Update("archived_at", time.Now().UTC()).Error)
	f, found, err = call(false)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, f.Draft)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(id)).Update(q.ArchivedAt, nil)
	require.NoError(t, err)
	_, err = publishBuiltPackageHTTP(t, m, ctx, &EditInput[PackagePublishBody]{Body: PackagePublishBody{ReauthProof: "test-publish-proof"}, EntityID: "3", ID: id.String(), Key: "confirmation-publish", IfMatch: `"1"`})
	require.NoError(t, err)
	f, found, err = call(false)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, f.Draft)
	require.Equal(t, 2, f.Version)
}

func TestDispatchInitiatorUsesExactOriginalEvent(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	created, err := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "fixture-channel", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "CHANNEL_REQUEST"}})
	require.NoError(t, err)
	id := created.Body.Data.ID
	q := query.BindDispatchProposal(db, m.names)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(id)).Update(q.DecidedBy, guid.ID(99))
	require.NoError(t, err)
	calls := 0
	source := events.SubjectHumanActor(func(_ context.Context, tx *gorm.DB, tenant, project guid.ID, kind string, subject guid.ID, typ string) (guid.ID, bool, error) {
		calls++
		require.NotNil(t, tx)
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		require.Equal(t, "clientwork", kind)
		require.Equal(t, id, subject)
		require.Equal(t, "clientwork.dispatch_proposed", typ)
		return 2, true, nil
	})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		fact, found, err := m.channelDispatchInitiatorOn(ctx, tx, 1, id, source)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, DispatchRequestInitiatorFact{ProjectID: 3, WorkItemID: 11, RequesterID: 2}, fact)
		_, found, err = m.channelDispatchInitiatorOn(ctx, tx, 9, id, source)
		require.NoError(t, err)
		require.False(t, found)
		_, found, err = m.channelDispatchInitiatorOn(ctx, tx, 1, id, func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID, string) (guid.ID, bool, error) {
			return 0, false, nil
		})
		require.NoError(t, err)
		require.False(t, found, "missing provenance must never fall back to the decider")
		_, _, err = m.channelDispatchInitiatorOn(ctx, tx, 1, id, nil)
		require.ErrorIs(t, err, ErrUnavailable)
		return nil
	}))
	require.Equal(t, 1, calls)
	_, _, err = m.channelDispatchInitiatorOn(ctx, db, 1, id, source)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(id)).Update(q.Trigger, "MANUAL_REQUEST")
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, err := m.channelDispatchInitiatorOn(ctx, tx, 1, id, source)
		require.NoError(t, err)
		require.False(t, found)
		return nil
	}))
	require.Equal(t, 1, calls)
}

func TestClarificationReplyRevisionUsesOnlyActualAnswers(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	opened, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.NoError(t, err)
	id := opened.Body.Data.ID
	check := func(tenant guid.ID, want int, exists bool) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			fact, found, err := m.clarificationReplySourceOn(testContext(t, 2), tx, tenant, id, true)
			require.NoError(t, err)
			require.Equal(t, exists, found)
			if found {
				require.Equal(t, want, fact.ReplyVersion)
				require.Equal(t, guid.ID(2), fact.RequesterID)
				require.Equal(t, guid.ID(3), fact.ProjectID)
			}
			return nil
		}))
	}
	check(1, 0, false)
	_, err = m.answerClarificationHTTP(testContext(t, 4), &EditInput[ClarificationAnswer]{EntityID: "3", ID: id.String(), IfMatch: `"1"`, Key: "fixture-first-reply", Body: ClarificationAnswer{QuestionNo: 1, Answer: "CSV"}})
	require.NoError(t, err)
	check(1, 1, true)
	check(2, 0, false)
	q := query.BindClarificationRound(db, m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(id)).Update(q.Version, 7)
	require.NoError(t, err)
	check(1, 1, true)
	_, err = m.answerClarificationHTTP(testContext(t, 4), &EditInput[ClarificationAnswer]{EntityID: "3", ID: id.String(), IfMatch: `"7"`, Key: "fixture-second-reply", Body: ClarificationAnswer{QuestionNo: 2, Answer: "UTC"}})
	require.NoError(t, err)
	check(1, 2, true)
}
