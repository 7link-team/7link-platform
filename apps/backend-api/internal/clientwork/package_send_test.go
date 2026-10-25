package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestPackageSendSourceFailuresNeverRecordSuccessfulTransport(t *testing.T) {
	for _, fault := range []string{"recipient", "client_read", "revoked", "recipient_capability", "recipient_check", "channel_capability", "channel_check", "history", "ids", "zero_message", "send_denied", "ledger", "audit"} {
		t.Run(fault, func(t *testing.T) {
			m, db, pkg := releaseNoteHarness(t)
			ctx := testContext(t, 2)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := publishBuiltPackage(t, m, ctx, tx, 3, pkg, 1); return e }))
			m.packageSender = collab.PackageDeliverySender{CheckOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return nil }, SendOn: func(_ context.Context, tx *gorm.DB, _ guid.ID, _ guid.ID, _ guid.ID, _ guid.ID, _ string) (guid.ID, error) {
				return 91, tx.Exec("INSERT INTO baseline_probe VALUES (91)").Error
			}}
			recipient := guid.ID(4)
			switch fault {
			case "recipient":
				recipient = 5
			case "client_read":
				m.clients = &testClients{clientErr: ErrUnavailable}
			case "revoked":
				m.clients = &testClients{revoked: true}
			case "recipient_capability":
				m.packageRecipient = nil
			case "recipient_check":
				m.packageRecipient = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrForbidden }
			case "channel_capability":
				m.packageSender.CheckOn = nil
			case "channel_check":
				m.packageSender.CheckOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return ErrForbidden }
			case "history":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("send_history_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.PackageSend.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "ids":
				m.ids = &unavailableIDs{source: m.ids, at: 1}
			case "zero_message":
				m.packageSender.SendOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, string) (guid.ID, error) {
					return 0, nil
				}
			case "send_denied":
				m.packageSender.SendOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, string) (guid.ID, error) {
					return 0, ErrForbidden
				}
			case "ledger":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("send_ledger_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.PackageSend.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "audit":
				m.audit = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.sendPackageOn(ctx, tx, 3, pkg, 2, 60, recipient); return e })
			require.Error(t, err)
			require.NoError(t, db.Callback().Query().Remove("send_history_failure"))
			require.NoError(t, db.Callback().Create().Remove("send_ledger_failure"))
			var n int64
			require.NoError(t, db.Table(m.names.PackageSend.String()).Count(&n).Error)
			require.Zero(t, n)
			// The fixture inbox row predates this attempt. No message from the
			// failed transport may survive its transaction.
			require.NoError(t, db.Table("baseline_probe").Where("id=91").Count(&n).Error)
			require.Zero(t, n)
		})
	}
}

func TestPackageSendPersistsFailuresRetriesAndAtomicMessageReference(t *testing.T) {
	m, db, projects, clients := harness(t)
	ctx := testContext(t, 2)
	created, err := m.create_packageHTTP(ctx, &CreateInput[PackageBody]{EntityID: "3", Key: "send-draft", Body: PackageBody{Title: "Fixed release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
	require.NoError(t, err)
	id := created.Body.Data.ID.String()
	publish := &EditInput[PackagePublishBody]{EntityID: "3", ID: id, IfMatch: `"1"`, Key: "send-publish", Body: PackagePublishBody{ReauthProof: "test-publish-proof"}}
	_, err = publishBuiltPackageHTTP(t, m, ctx, publish)
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE send_message_probe(id BIGINT)").Error)
	denied, fail := false, true
	m.packageSender = collab.PackageDeliverySender{CheckOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error {
		if denied {
			return collab.ErrNotFound
		}
		return nil
	}, SendOn: func(_ context.Context, tx *gorm.DB, tenant, project, channel, recipient guid.ID, body string) (guid.ID, error) {
		require.Equal(t, guid.ID(4), recipient)
		require.Contains(t, body, "Manifest SHA-256:")
		require.NoError(t, tx.Exec("INSERT INTO send_message_probe VALUES (73)").Error)
		if fail {
			return 0, collab.ErrUnavailable
		}
		return 73, nil
	}}
	in := &EditInput[PackageSendBody]{EntityID: "3", ID: id, IfMatch: `"2"`, Key: "send-first", Body: PackageSendBody{ChannelID: 60}}
	first, err := m.packageSendHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "FAILED", first.Body.Data.State)
	var n int64
	require.NoError(t, db.Table("send_message_probe").Count(&n).Error)
	require.Zero(t, n, "savepoint rolls failed send effects back")
	replay, err := m.packageSendHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	denied = true
	_, err = m.packageSendHTTP(ctx, in)
	require.Error(t, err, "channel authority is current on replay")
	denied = false
	clients.revoked = true
	_, err = m.packageSendHTTP(ctx, in)
	require.Error(t, err)
	clients.revoked = false
	projects.deny = true
	_, err = m.packageSendHTTP(ctx, in)
	require.Error(t, err)
	projects.deny = false
	fail = false
	in.Key = "send-retry"
	publisher := m.events
	m.events = failPublisher{}
	_, err = m.packageSendHTTP(ctx, in)
	require.Error(t, err)
	m.events = publisher
	require.NoError(t, db.Table("send_message_probe").Count(&n).Error)
	require.Zero(t, n, "outbox failure rolls back actual send and ledger")
	sent, err := m.packageSendHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "SUCCEEDED", sent.Body.Data.State)
	var sends []model.PackageSend
	require.NoError(t, db.Table(m.names.PackageSend.String()).Order("attempt_no").Find(&sends).Error)
	require.Len(t, sends, 2)
	require.Equal(t, 1, sends[0].Attempt)
	require.Equal(t, 2, sends[1].Attempt)
	require.Equal(t, guid.ID(73), *sends[1].MessageID)
	require.Equal(t, sends[0].ContentHash, sends[1].ContentHash)
	replay, err = m.packageSendHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	in.Key = "duplicate-success"
	_, err = m.packageSendHTTP(ctx, in)
	require.Error(t, err, "successful local sends cannot be duplicated under a fresh key")
	page, err := m.packageSendsHTTP(ctx, &PackageTimelineInput{ListInput: ListInput{EntityID: "3", Limit: 1}, ID: id})
	require.NoError(t, err)
	require.True(t, page.Body.Data.HasMore)
	next, err := m.packageSendsHTTP(ctx, &PackageTimelineInput{ListInput: ListInput{EntityID: "3", Limit: 1, Cursor: page.Body.Data.NextCursor}, ID: id})
	require.NoError(t, err)
	require.Len(t, next.Body.Data.Items, 1)
	require.Equal(t, "SUCCEEDED", next.Body.Data.Items[0].State)
	_, err = m.packageSendsHTTP(testContext(t, 4), &PackageTimelineInput{ListInput: ListInput{EntityID: "3", Limit: 20}, ID: id})
	require.Error(t, err, "external clients cannot read private transport ledger")
}
func TestPackagePublishRequiresBoundProofAndReplayRechecksRecipient(t *testing.T) {
	m, _, _, clients := harness(t)
	ctx := testContext(t, 2)
	created, err := m.create_packageHTTP(ctx, &CreateInput[PackageBody]{EntityID: "3", Key: "proof-draft", Body: PackageBody{Title: "Fixed release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
	require.NoError(t, err)
	in := &EditInput[PackagePublishBody]{EntityID: "3", ID: created.Body.Data.ID.String(), IfMatch: `"1"`, Key: "proof-publish", Cookie: "session"}
	_, err = publishBuiltPackageHTTP(t, m, ctx, in)
	require.Error(t, err)
	calls := 0
	m.acceptanceProof = func(_ context.Context, _ *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		calls++
		require.Equal(t, guid.ID(2), actor)
		require.Equal(t, "session", cookie)
		require.Equal(t, "delivery_send", purpose)
		require.Equal(t, "package:"+in.ID, target)
		if proof != "verified" {
			return authn.ErrInvalidCredentials
		}
		return nil
	}
	in.Body.ReauthProof = "unverified"
	_, err = publishBuiltPackageHTTP(t, m, ctx, in)
	require.Error(t, err)
	in.Body.ReauthProof = "verified"
	_, err = publishBuiltPackageHTTP(t, m, ctx, in)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	in.Body.ReauthProof = "renewed-proof"
	in.Cookie = "renewed-session"
	replay, err := publishBuiltPackageHTTP(t, m, ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, 2, calls, "proof is consumed only on the first transactional effect")
	clients.revoked = true
	_, err = publishBuiltPackageHTTP(t, m, ctx, in)
	require.Error(t, err)
}
