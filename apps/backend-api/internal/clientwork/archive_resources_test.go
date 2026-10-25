package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestRubricConnectorRetainsOwningPackageErrorsAndExactFrozenAuthority(t *testing.T) {
	m, db, pkg := releaseNoteHarness(t)
	ctx := testContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := publishBuiltPackage(t, m, ctx, tx, 3, pkg, 1); return e }))
	view, err := m.get_packageHTTP(ctx, &ObjectInput{EntityID: "3", ID: pkg.String()})
	require.NoError(t, err)
	var read func(context.Context, *gorm.DB, delivery.RubricClientRequest) (delivery.RubricClientTarget, error)
	s := module.NewServices()
	require.NoError(t, module.Provide[delivery.RubricClientConnector](s, delivery.RubricClientConnector{Bind: func(f delivery.RubricClientReader) error { read = f; return nil }}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.injectRubric(s))
	require.NotNil(t, read)
	var pack model.DeliveryPackage
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&pack).Error)
	for _, fault := range []string{"human", "invalid", "stage", "signer", "missing", "archived", "manifest", "hash", "refs", "unavailable"} {
		t.Run(fault, func(t *testing.T) {
			in := delivery.RubricClientRequest{ProjectID: 3, PackageID: pkg, ItemID: view.Body.Data.Items[0].ItemID, Version: 2, Stage: "HUMAN_REVIEW", Write: true}
			actor := guid.ID(2)
			want := error(nil)
			switch fault {
			case "invalid":
				in.ItemID = "bad"
				want = delivery.ErrInvalidArgument
			case "stage":
				in.Stage = "CLIENT_ACCEPTANCE"
				want = delivery.ErrWorkForbidden
			case "signer":
				actor = 4
				in.Stage = "CLIENT_ACCEPTANCE"
				m.clientSigners.ContactOn = nil
				want = delivery.ErrUnavailable
			case "missing":
				in.PackageID = 999
				want = delivery.ErrProjectNotFound
			case "archived":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("archived_at", now()).Error)
				want = delivery.ErrResultState
			case "manifest":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("manifest", "[]").Error)
				want = delivery.ErrUnavailable
			case "hash":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("content_hash", strings.Repeat("b", 64)).Error)
				want = delivery.ErrResultState
			case "refs":
				var manifest packageManifest
				require.NoError(t, json.Unmarshal([]byte(pack.Manifest), &manifest))
				manifest.Refs[0].EvidenceID = 0
				raw, e := json.Marshal(manifest)
				require.NoError(t, e)
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("manifest", string(raw)).Error)
				want = delivery.ErrUnavailable
			case "unavailable":
				m.projects = nil
				want = delivery.ErrUnavailable
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				out, e := read(testContext(t, actor), tx, in)
				if fault == "human" {
					require.Equal(t, "HUMAN", out.ActorKind)
					require.Equal(t, "permission:delivery_compose", out.Authority)
					require.Equal(t, []guid.ID{30}, out.EvidenceIDs)
				}
				return e
			})
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Updates(map[string]any{"manifest": pack.Manifest, "content_hash": pack.ContentHash, "archived_at": nil}).Error)
		})
	}
}

func TestArchiveResourcesPropagateEachSourceReadFailureWithoutReadiness(t *testing.T) {
	m, db, pkg := releaseNoteHarness(t)
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("state", "ACCEPTED").Error)
	for _, table := range []string{m.names.DeliveryPackage.String(), m.names.PackageReceipt.String(), m.names.AcceptanceReminder.String(), m.names.ClarificationRound.String(), m.names.PreviewShare.String(), m.names.DispatchProposal.String(), m.names.Sheets.String(), m.names.ChangeRequest.String(), m.names.ToleranceException.String()} {
		t.Run(table, func(t *testing.T) {
			failure := errors.New("archive source unavailable")
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("archive_source_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(failure)
				}
			}))
			err := db.Transaction(func(tx *gorm.DB) error {
				rows, e := m.projectArchiveResourcesOn(testContext(t, 2), tx, 1, 3)
				require.Nil(t, rows)
				return e
			})
			require.ErrorIs(t, err, failure)
			require.NoError(t, db.Callback().Query().Remove("archive_source_failure"))
		})
	}
}

func readArchiveResources(t *testing.T, m *Module, db *gorm.DB, tenant, project guid.ID) []delivery.ProjectArchiveResource {
	t.Helper()
	var rows []delivery.ProjectArchiveResource
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		rows, err = m.projectArchiveResourcesOn(t.Context(), tx, tenant, project)
		return err
	}))
	return rows
}
func TestProjectArchiveClientPackageRequiresExactReceipt(t *testing.T) {
	m, db, _, _ := harness(t)
	var receipt Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		receipt, err = m.createPackageOn(testContext(t, 2), tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
		return err
	}))
	resources := readArchiveResources(t, m, db, 1, 3)
	require.Len(t, resources, 1)
	require.False(t, resources[0].Ready)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		receipt, err = publishBuiltPackage(t, m, testContext(t, 2), tx, 3, receipt.ID, receipt.Version)
		return err
	}))
	require.False(t, readArchiveResources(t, m, db, 1, 3)[0].Ready)
	view, err := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: receipt.ID.String()})
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		receipt, err = m.decidePackageOn(testContext(t, 4), tx, 3, receipt.ID, receipt.Version, PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: view.Body.Data.ContentHash, Decision: "ACCEPT", Reason: "Received and checked"})
		return err
	}))
	require.True(t, readArchiveResources(t, m, db, 1, 3)[0].Ready)
	r := query.BindPackageReceipt(db, m.names)
	_, err = r.WithContext(t.Context()).Where(r.PackageID.Eq(receipt.ID)).UpdateSimple(r.ContentHash.Value("stale-content"))
	require.NoError(t, err)
	require.False(t, readArchiveResources(t, m, db, 1, 3)[0].Ready)
	require.Empty(t, readArchiveResources(t, m, db, 2, 3))
	require.Empty(t, readArchiveResources(t, m, db, 1, 4))
	_, err = m.projectArchiveResourcesOn(t.Context(), db, 1, 3)
	require.ErrorIs(t, err, ErrInvalid)
}
func TestProjectArchiveClientShareRequiresRevocationReceipt(t *testing.T) {
	m, db, _, _ := harness(t)
	_, share := previewShareFixture(t, m, db)
	rows := readArchiveResources(t, m, db, 1, 3)
	require.Len(t, rows, 1)
	require.Equal(t, "PREVIEW_SHARE", rows[0].Kind)
	require.False(t, rows[0].Ready)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.decideShareOn(testContext(t, 6), tx, 3, share.ID, 1, PreviewShareDecision{Decision: "REVOKE", Reason: "Explicit archive disposition"})
		return err
	}))
	require.True(t, readArchiveResources(t, m, db, 1, 3)[0].Ready)
}
func TestProjectArchiveClientClarificationRequiresClosedRound(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	opened, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.NoError(t, err)
	rows := readArchiveResources(t, m, db, 1, 3)
	require.Len(t, rows, 1)
	require.Equal(t, "CLIENT_CLARIFICATION", rows[0].Kind)
	require.False(t, rows[0].Ready)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.cancelClarificationOn(testContext(t, 2), tx, 3, opened.Body.Data.ID, 1, "Explicitly cancelled for archival") //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return err
	}))
	require.True(t, readArchiveResources(t, m, db, 1, 3)[0].Ready)
}

func archiveKind(t *testing.T, rows []delivery.ProjectArchiveResource, kind string) []delivery.ProjectArchiveResource {
	t.Helper()
	var out []delivery.ProjectArchiveResource
	for _, row := range rows {
		if row.Kind == kind {
			out = append(out, row)
		}
	}
	return out
}
func TestProjectArchiveAutomaticDispatchExpiryNeedsNoHumanDecision(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	created, err := m.createDispatchHTTP(testContext(t, 2), &CreateInput[DispatchBody]{EntityID: "3", Key: "archive-dispatch-expiry", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
	require.NoError(t, err)
	rows := archiveKind(t, readArchiveResources(t, m, db, 1, 3), "DISPATCH_PROPOSAL")
	require.Len(t, rows, 1)
	require.False(t, rows[0].Ready)
	q := query.BindDispatchProposal(db, m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(created.Body.Data.ID)).Update(q.ExpiresAt, now().Add(-time.Minute))
	require.NoError(t, err)
	require.NoError(t, m.expireDue(t.Context(), 1, now()))
	saved, err := q.WithContext(t.Context()).Where(q.ID.Eq(created.Body.Data.ID)).First()
	require.NoError(t, err)
	require.Nil(t, saved.DecidedAt)
	rows = archiveKind(t, readArchiveResources(t, m, db, 1, 3), "DISPATCH_PROPOSAL")
	require.True(t, rows[0].Ready)
	require.Equal(t, "EXPIRED", rows[0].State)
}
func TestProjectArchiveClientSignoffAndChangeRequireDisposition(t *testing.T) {
	m, db, _, _ := harness(t)
	sheet := draftAndPublish(t, m, db)
	require.Len(t, archiveKind(t, readArchiveResources(t, m, db, 1, 3), "CLIENT_SIGNOFF"), 1)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.signSheetOn(testContext(t, 4), tx, 3, sheet.ID, sheet.Version, SignoffBody{1, sheet.ContentHash, "SIGN", "Reviewed"})
		return err
	}))
	require.Empty(t, archiveKind(t, readArchiveResources(t, m, db, 1, 3), "CLIENT_SIGNOFF"))
	var change Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		change, err = m.createChangeOn(testContext(t, 4), tx, 3, ChangeBody{sheet.ID, 1, "Requested new scope"})
		return err
	}))
	rows := archiveKind(t, readArchiveResources(t, m, db, 1, 3), "CLIENT_CHANGE")
	require.Len(t, rows, 1)
	require.False(t, rows[0].Ready)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		change, err = m.actChangeOn(testContext(t, 2), tx, 3, change.ID, change.Version, ChangeAction{Action: "ASSESS", Impact: "Actual impact"})
		return err
	}))
	rows = archiveKind(t, readArchiveResources(t, m, db, 1, 3), "CLIENT_CHANGE")
	require.Equal(t, "AWAITING_CONFIRMATION", rows[0].State)
	require.False(t, rows[0].Ready)
	view, err := m.get_changeHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: change.ID.String()})
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.actChangeOn(testContext(t, 4), tx, 3, change.ID, change.Version, ChangeAction{Action: "REJECT", ProposalHash: view.Body.Data.ProposalHash})
		return err
	}))
	require.Empty(t, archiveKind(t, readArchiveResources(t, m, db, 1, 3), "CLIENT_CHANGE"))
	require.Empty(t, readArchiveResources(t, m, db, 2, 3))
	require.Empty(t, readArchiveResources(t, m, db, 1, 4))
}
func TestProjectArchiveToleranceExceptionRequiresHumanDecisionReceipt(t *testing.T) {
	m, db, _, _ := harness(t)
	tol := toleranceFixture(t, m, db, "REQUIREMENT", 10)
	ex := raisedException(t, m, db, tol)
	rows := archiveKind(t, readArchiveResources(t, m, db, 1, 3), "TOLERANCE_EXCEPTION")
	require.Len(t, rows, 1)
	require.False(t, rows[0].Ready)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reportExceptionOn(testContext(t, 2), tx, 3, ex.ID, 1, ExceptionReport{Situation: "Forecast exceeds commitment", Options: []ExceptionOption{{Decision: "HOLD", Impact: "Wait"}}, Recommendation: "HOLD"})
		return err
	}))
	require.False(t, archiveKind(t, readArchiveResources(t, m, db, 1, 3), "TOLERANCE_EXCEPTION")[0].Ready)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.decideExceptionOn(testContext(t, 2), tx, 3, ex.ID, 2, "cookie", ExceptionDecisionBody{Decision: "HOLD", Reason: "Human reviewed", ResumeCondition: "Wait for input", ReauthProof: "secret-proof"})
		return err
	}))
	require.True(t, archiveKind(t, readArchiveResources(t, m, db, 1, 3), "TOLERANCE_EXCEPTION")[0].Ready)
	q := query.BindToleranceException(db, m.names)
	_, err := q.WithContext(t.Context()).Where(q.ID.Eq(ex.ID)).UpdateSimple(q.DecidedAt.Null())
	require.NoError(t, err)
	require.False(t, archiveKind(t, readArchiveResources(t, m, db, 1, 3), "TOLERANCE_EXCEPTION")[0].Ready)
}

func TestRubricPackageUsesPublishedPublicItemAndCurrentSigner(t *testing.T) {
	m, db, projects, _ := harness(t)
	internal, client := testContext(t, 2), testContext(t, 4)
	var pkg Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		pkg, e = m.createPackageOn(internal, tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Actual artifacts", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		pkg, e = publishBuiltPackage(t, m, internal, tx, 3, pkg.ID, pkg.Version)
		return e
	}))
	view, e := m.get_packageHTTP(client, &ObjectInput{EntityID: "3", ID: pkg.ID.String()})
	require.NoError(t, e)
	in := delivery.RubricClientRequest{ProjectID: 3, PackageID: pkg.ID, ItemID: view.Body.Data.Items[0].ItemID, Version: pkg.Version, Stage: "CLIENT_ACCEPTANCE", Write: true}
	var out delivery.RubricClientTarget
	read := func() error {
		return db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.rubricPackageOn(client, tx, in); return e })
	}
	require.NoError(t, read())
	require.Equal(t, guid.ID(11), out.WorkItemID)
	require.Equal(t, guid.ID(20), out.ResultID)
	require.Equal(t, []guid.ID{30}, out.EvidenceIDs)
	require.Equal(t, "CLIENT", out.ActorKind)
	require.Equal(t, view.Body.Data.ContentHash, out.Hash)
	in.Version--
	require.Error(t, read())
	in.Version++
	in.Stage = "HUMAN_REVIEW"
	require.Error(t, read())
	in.Stage = "CLIENT_ACCEPTANCE"
	in.ItemID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.Error(t, read())
	in.ItemID = view.Body.Data.Items[0].ItemID
	projects.deny = true
	require.Error(t, read())
	projects.deny = false
	require.True(t, rubricRoute("GET", []string{"entities", "3", "delivery-packages", pkg.ID.String(), "rubric-scores"}))
	require.False(t, rubricRoute("DELETE", []string{"entities", "3", "delivery-packages", pkg.ID.String(), "rubric-scores"}))
}
