package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"strings"
	"testing"
)

func TestRecordedAcceptanceSourceFailuresPreserveProofPinsAndPackage(t *testing.T) {
	for _, fault := range []string{"contact_capability", "recipient_changed", "client_read", "client_revoked", "proof_capability", "files_capability", "inspect", "invalid_candidate", "pin_mismatch", "ids"} {
		t.Run(fault, func(t *testing.T) {
			m, db, in := recordedFixture(t)
			ctx := testContext(t, 2)
			switch fault {
			case "contact_capability":
				m.recordedContact = nil
			case "recipient_changed":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", in.ID).Update("recipient_id", 5).Error)
			case "client_read":
				m.clients = &testClients{clientErr: ErrUnavailable}
			case "client_revoked":
				m.clients = &testClients{revoked: true}
			case "proof_capability":
				m.acceptanceProof = nil
			case "files_capability":
				m.answerFiles = nil
			case "inspect":
				m.answerFiles = testAnswerFiles{inspect: func(context.Context, *gorm.DB, guid.ID) (files.PinnedCandidate, error) {
					return files.PinnedCandidate{}, ErrUnavailable
				}}
			case "invalid_candidate":
				m.answerFiles = testAnswerFiles{inspect: func(_ context.Context, _ *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
					candidate := answerCandidate(id)
					candidate.Size = 0
					return candidate, nil
				}}
			case "pin_mismatch":
				m.answerFiles = testAnswerFiles{inspect: func(_ context.Context, _ *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
					return answerCandidate(id), nil
				}, pin: func(context.Context, *gorm.DB, files.ResourceBindingInput) (files.PinnedResource, error) {
					return files.PinnedResource{}, nil
				}}
			case "ids":
				m.ids = &unavailableIDs{source: m.ids, at: 1}
			}
			out, err := m.recordedAcceptanceHTTP(ctx, in)
			require.Error(t, err)
			require.Nil(t, out)
			for _, table := range []string{m.names.PackageReceipt.String(), "offline_probe"} {
				var n int64
				require.NoError(t, db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
			var pack model.DeliveryPackage
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", in.ID).Take(&pack).Error)
			require.Equal(t, "PUBLISHED", pack.State)
			require.Equal(t, 2, pack.Version)
		})
	}
}

func TestRecordedEvidenceTamperAndReadFailureCannotReturnBytes(t *testing.T) {
	for _, fault := range []string{"query", "missing_binding", "evidence_json", "open", "binding", "hash", "reader"} {
		t.Run(fault, func(t *testing.T) {
			m, db, in := recordedFixture(t)
			_, err := m.recordedAcceptanceHTTP(testContext(t, 2), in)
			require.NoError(t, err)
			var receipt model.PackageReceipt
			require.NoError(t, db.Table(m.names.PackageReceipt.String()).Take(&receipt).Error)
			switch fault {
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("evidence_read_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.PackageReceipt.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "missing_binding":
				require.NoError(t, db.Table(m.names.PackageReceipt.String()).Where("id=?", receipt.ID).Update("evidence_binding_id", nil).Error)
			case "evidence_json":
				require.NoError(t, db.Table(m.names.PackageReceipt.String()).Where("id=?", receipt.ID).Update("evidence_json", "{").Error)
			default:
				original := m.answerFiles
				m.answerFiles = testAnswerFiles{open: func(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedResourceRead, error) {
					if fault == "open" {
						return files.PinnedResourceRead{}, ErrUnavailable
					}
					read, e := original.OpenOn(ctx, tx, id)
					if fault == "binding" {
						read.BindingID++
					}
					if fault == "hash" {
						read.SHA256 = strings.Repeat("f", 64)
					}
					if fault == "reader" {
						require.NoError(t, read.Reader.Close())
						read.Reader = nil
					}
					return read, e
				}}
			}
			out, err := m.recordedEvidenceHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: in.ID})
			require.Error(t, err)
			require.Nil(t, out)
		})
	}
}

func TestAcceptanceEvidenceResourceChecksExactOwnerAndCurrentPackageVisibility(t *testing.T) {
	m, db, in := recordedFixture(t)
	id, err := guid.RequireID(in.ID)
	require.NoError(t, err)
	for _, tc := range []struct {
		actor   guid.ID
		owner   files.ResourceOwner
		allowed bool
	}{
		{2, files.ResourceOwner{Scope: "entity", ID: 3}, true},
		{4, files.ResourceOwner{Scope: "entity", ID: 3}, true},
		{5, files.ResourceOwner{Scope: "entity", ID: 3}, false},
		{2, files.ResourceOwner{Scope: "entity", ID: 9}, false},
		{2, files.ResourceOwner{Scope: "tenant", ID: 3}, false},
	} {
		err := db.Transaction(func(tx *gorm.DB) error {
			return m.acceptanceRecordResourceOn(testContext(t, tc.actor), tx, id, tc.owner)
		})
		if tc.allowed {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		return m.acceptanceRecordResourceOn(testContext(t, 2), tx, 999, files.ResourceOwner{Scope: "entity", ID: 3})
	}))
	for _, bad := range []*ObjectInput{{EntityID: "bad", ID: in.ID}, {EntityID: "3", ID: "bad"}, {EntityID: "3", ID: "999"}} {
		out, err := m.recordedEvidenceHTTP(testContext(t, 2), bad)
		require.Error(t, err)
		require.Nil(t, out)
	}
}

func recordedFixture(t *testing.T) (*Module, *gorm.DB, *RecordedAcceptanceInput) {
	t.Helper()
	m, db, _, _ := harness(t)
	created, err := m.create_packageHTTP(testContext(t, 2), &CreateInput[PackageBody]{EntityID: "3", Key: "offline-package", Body: PackageBody{Title: "Client release", ReleaseNotes: "Final Human release notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
	require.NoError(t, err)
	id := created.Body.Data.ID.String()
	_, err = publishBuiltPackageHTTP(t, m, testContext(t, 2), &EditInput[PackagePublishBody]{Body: PackagePublishBody{ReauthProof: "test-publish-proof"}, EntityID: "3", ID: id, Key: "offline-publish", IfMatch: `"1"`})
	require.NoError(t, err)
	view, err := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: id})
	require.NoError(t, err)
	m.recordedContact = func(_ context.Context, _ *gorm.DB, tenant, project, recipient guid.ID) (guid.ID, error) {
		if tenant != 1 || project != 3 || recipient != 4 {
			return 0, ErrForbidden
		}
		return 44, nil
	}
	require.NoError(t, db.Exec("CREATE TABLE offline_probe (kind TEXT NOT NULL)").Error)
	m.acceptanceProof = func(_ context.Context, tx *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		if actor != 2 || cookie != "session" || proof != "fresh" || purpose != "acceptance_record" || target != "package:"+id {
			return authn.ErrInvalidCredentials
		}
		return tx.Exec("INSERT INTO offline_probe VALUES ('proof')").Error
	}
	var pinned files.PinnedResource
	m.answerFiles = testAnswerFiles{inspect: func(_ context.Context, _ *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
		return answerCandidate(id), nil
	}, pin: func(_ context.Context, tx *gorm.DB, in files.ResourceBindingInput) (files.PinnedResource, error) {
		if in.Purpose != "acceptance_record" || in.ResourceKind != "client_package" || in.ActorID != 2 {
			return files.PinnedResource{}, ErrInvalid
		}
		if err := tx.Exec("INSERT INTO offline_probe VALUES ('pin')").Error; err != nil {
			return files.PinnedResource{}, err
		}
		pinned = answerCandidate(in.FileID).PinnedResource
		pinned.BindingID = in.ID
		pinned.ResourceID = in.ResourceID
		pinned.Purpose = in.Purpose
		pinned.ResourceKind = in.ResourceKind
		return pinned, nil
	}, open: func(context.Context, *gorm.DB, guid.ID) (files.PinnedResourceRead, error) {
		return files.PinnedResourceRead{PinnedResource: pinned, Reader: io.NopCloser(strings.NewReader("proof"))}, nil
	}}
	body := RecordedAcceptanceBody{PackageDecision: PackageDecision{SignerName: "Client signing person", Confirmed: true, ContentHash: view.Body.Data.ContentHash, Reason: "Offline signed client decision", Items: []PackageItemDecision{{ItemID: view.Body.Data.Items[0].ItemID, Result: "ACCEPTED"}}}, EvidenceFileID: 91, ReauthProof: "fresh"}
	return m, db, &RecordedAcceptanceInput{EditInput: EditInput[RecordedAcceptanceBody]{EntityID: "3", ID: id, Key: "record-once", IfMatch: `"2"`, Body: body}, Cookie: "session"}
}

func TestRecordedAcceptanceFreezesClientRecorderEvidenceAndReauthorizesReplay(t *testing.T) {
	m, db, in := recordedFixture(t)
	ctx := testContext(t, 2)
	first, err := m.recordedAcceptanceHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "ACCEPTED", first.Body.Data.State)
	var r model.PackageReceipt
	require.NoError(t, db.Table(m.names.PackageReceipt.String()).Take(&r).Error)
	require.EqualValues(t, 4, r.ActorID)
	require.EqualValues(t, 2, *r.RecordedBy)
	require.EqualValues(t, 44, *r.SignerContactID)
	require.Equal(t, "RECORDED", r.Source)
	require.NotNil(t, r.EvidenceBindingID)
	require.Contains(t, *r.ReceiptJSON, `"source":"RECORDED"`)
	require.Contains(t, *r.ReceiptJSON, `"recorded_by":"2"`)
	require.NotContains(t, *r.ReceiptJSON, "fresh")
	sum := sha256.Sum256([]byte(*r.ReceiptJSON))
	require.Equal(t, hex.EncodeToString(sum[:]), *r.ReceiptSHA256)
	view, err := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: in.ID})
	require.NoError(t, err)
	require.Equal(t, "RECORDED", view.Body.Data.Signature.Source)
	require.EqualValues(t, 2, *view.Body.Data.Signature.RecordedBy)
	require.EqualValues(t, 91, view.Body.Data.Signature.Evidence.FileID)
	_, err = m.recordedEvidenceHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: in.ID})
	require.NoError(t, err)
	retry := *in
	retry.Body.ReauthProof = "renewed-proof"
	retry.Cookie = "renewed-session"
	replay, err := m.recordedAcceptanceHTTP(ctx, &retry)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	var count int64
	require.NoError(t, db.Table("offline_probe").Count(&count).Error)
	require.EqualValues(t, 2, count, "replay must not consume proof or pin twice")
	m.recordedContact = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error) { return 0, ErrForbidden }
	_, err = m.recordedAcceptanceHTTP(ctx, in)
	require.Error(t, err)
}

func TestRecordedAcceptanceFailuresAreAtomicAndCannotReplaceClientAcceptance(t *testing.T) {
	for _, stage := range []string{"proof", "missing_evidence", "missing_items", "unconfirmed", "wrong_hash", "audit", "outbox", "pin", "client", "agent", "stale"} {
		t.Run(stage, func(t *testing.T) {
			m, db, in := recordedFixture(t)
			ctx := testContext(t, 2)
			switch stage {
			case "proof":
				in.Body.ReauthProof = "wrong"
			case "missing_evidence":
				in.Body.EvidenceFileID = 0
			case "missing_items":
				in.Body.Items = nil
			case "unconfirmed":
				in.Body.Confirmed = false
			case "wrong_hash":
				in.Body.ContentHash = strings.Repeat("f", 64)
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			case "outbox":
				m.events = expiryPublisher{Publisher: m.events, publish: func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
					return 0, errors.New("outbox unavailable")
				}}
			case "pin":
				m.answerFiles = testAnswerFiles{inspect: func(_ context.Context, _ *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
					return answerCandidate(id), nil
				}, pin: func(context.Context, *gorm.DB, files.ResourceBindingInput) (files.PinnedResource, error) {
					return files.PinnedResource{}, errors.New("pin unavailable")
				}}
			case "client":
				ctx = testContext(t, 4)
			case "agent":
				ctx = testContext(t, 7)
			case "stale":
				in.IfMatch = `"1"`
			}
			_, err := m.recordedAcceptanceHTTP(ctx, in)
			require.Error(t, err)
			var count int64
			require.NoError(t, db.Table(m.names.PackageReceipt.String()).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Table("offline_probe").Count(&count).Error)
			require.Zero(t, count, "proof and pin roll back with receipt")
			var pkg model.DeliveryPackage
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Take(&pkg).Error)
			require.Equal(t, "PUBLISHED", pkg.State)
			require.Equal(t, 2, pkg.Version)
		})
	}
}
