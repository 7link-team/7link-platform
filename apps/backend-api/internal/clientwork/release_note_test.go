package clientwork

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func releaseNoteHarness(t *testing.T) (*Module, *gorm.DB, guid.ID) {
	t.Helper()
	m, db, _, _ := harness(t)
	m.releaseDrafters = delivery.ReleaseNoteDrafters{
		SelectOn: func(ctx context.Context, tx *gorm.DB, tenant, p guid.ID) (guid.ID, error) {
			_, e := m.authorize(ctx, tx, p, "delivery_compose", false)
			return 8, e
		},
		AuthorizeOn: func(_ context.Context, _ *gorm.DB, tenant, p, agent guid.ID) error {
			if tenant != 1 || p != 3 || agent != 8 {
				return ErrForbidden
			}
			return nil
		},
		WriterOn: func(ctx context.Context, _ *gorm.DB, tenant, p guid.ID) error {
			if tenant != 1 || p != 3 || authn.PrincipalIDFromContext(ctx) != 8 {
				return ErrForbidden
			}
			return nil
		},
	}
	m.triageInbox = agents.InboxAppender{AppendOn: func(_ context.Context, tx *gorm.DB, tenant, principal guid.ID, in agents.InboxReference) (int64, error) {
		require.EqualValues(t, 8, principal)
		require.Equal(t, "release-note:"+in.SubjectID.String(), in.SourceEventID)
		return 1, tx.Exec("INSERT INTO baseline_probe VALUES (?)", in.SubjectID).Error
	}}
	created, e := m.create_packageHTTP(testContext(t, 2), &CreateInput[PackageBody]{EntityID: "3", Key: "package", Body: PackageBody{Title: "Release", ReleaseNotes: "Manual notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
	require.NoError(t, e)
	return m, db, created.Body.Data.ID
}
func releaseNoteRequest(t *testing.T, m *Module, pkg guid.ID) *CommandOutput {
	t.Helper()
	r, e := m.requestReleaseNoteHTTP(testContext(t, 2), &EditInput[ReleaseNoteRequest]{EntityID: "3", ID: pkg.String(), Key: "request", IfMatch: "\"1\"", Body: ReleaseNoteRequest{Note: "Summarize verified changes"}})
	require.NoError(t, e)
	return r
}
func releaseNoteInput(t *testing.T, m *Module, pkg, draft guid.ID) *ReleaseNoteInput {
	t.Helper()
	list, e := m.listReleaseNotesHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: pkg.String()})
	require.NoError(t, e)
	require.Len(t, list.Body.Data, 1)
	row := list.Body.Data[0]
	require.Equal(t, draft, row.ID)
	return &ReleaseNoteInput{EditInput: EditInput[ReleaseNoteText]{EntityID: "3", ID: pkg.String(), Key: "write", IfMatch: "\"1\"", Body: ReleaseNoteText{SourceFingerprint: row.SourceFingerprint, Content: "Agent text", PackageVersion: 1}}, DraftID: draft.String()}
}
func TestReleaseNoteHumanEditedAdoptionAndImmutableSource(t *testing.T) {
	m, db, pkg := releaseNoteHarness(t)
	r := releaseNoteRequest(t, m, pkg)
	in := releaseNoteInput(t, m, pkg, r.Body.Data.ID)
	require.NotEqual(t, releaseFingerprint(""), in.Body.SourceFingerprint)
	_, e := m.writeReleaseNoteHTTP(testContext(t, 2), (*ReleaseNoteWriteInput)(in))
	require.Error(t, e)
	_, e = m.writeReleaseNoteHTTP(testContext(t, 4), (*ReleaseNoteWriteInput)(in))
	require.Error(t, e)
	out, e := m.writeReleaseNoteHTTP(testContext(t, 8), (*ReleaseNoteWriteInput)(in))
	require.NoError(t, e)
	require.Equal(t, "DRAFT", out.Body.Data.State)
	out, e = m.writeReleaseNoteHTTP(testContext(t, 8), (*ReleaseNoteWriteInput)(in))
	require.NoError(t, e)
	require.Equal(t, "true", out.Replayed)
	view, e := m.get_packageHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: pkg.String()})
	require.NoError(t, e)
	require.Equal(t, "Manual notes", view.Body.Data.Content.ReleaseNotes)
	in.IfMatch = "\"2\""
	in.Key = "adopt"
	_, e = m.adoptReleaseNoteHTTP(testContext(t, 2), in)
	require.Error(t, e, "unchanged Agent text cannot count as Human final edit")
	in.Body.Content = "Human verified final text"
	publisher := m.events
	m.events = failPublisher{}
	_, e = m.adoptReleaseNoteHTTP(testContext(t, 2), in)
	require.Error(t, e)
	var stored model.DeliveryPackage
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&stored).Error)
	require.Equal(t, 1, stored.Version)
	m.events = publisher
	out, e = m.adoptReleaseNoteHTTP(testContext(t, 2), in)
	require.NoError(t, e)
	require.Equal(t, "ADOPTED", out.Body.Data.State)
	out, e = m.adoptReleaseNoteHTTP(testContext(t, 2), in)
	require.NoError(t, e)
	require.Equal(t, "true", out.Replayed)
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&stored).Error)
	require.Equal(t, 2, stored.Version)
	require.Equal(t, in.Body.Content, stored.ReleaseNotes)
	var row model.ReleaseNoteDraft
	require.NoError(t, db.Table(m.names.ReleaseNoteDraft.String()).Where("id=?", r.Body.Data.ID).Take(&row).Error)
	require.Equal(t, "Agent text", *row.Content)
	require.EqualValues(t, 2, *row.AdoptedBy)
	_, e = m.listReleaseNotesHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: pkg.String()})
	require.Error(t, e)
	m.releaseDrafters.WriterOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return ErrForbidden }
	in.Key = "write"
	in.IfMatch = "\"1\""
	in.Body.Content = "Agent text"
	_, e = m.writeReleaseNoteHTTP(testContext(t, 8), (*ReleaseNoteWriteInput)(in))
	require.Error(t, e, "revoked Agent cannot replay")
}
func TestReleaseNoteRejectsStaleTamperedWrongActorAndRollsBackInbox(t *testing.T) {
	for _, scenario := range []string{"stale", "tamper", "fingerprint", "wrong_actor", "inbox", "audit", "outbox", "limit", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, pkg := releaseNoteHarness(t)
			if scenario == "inbox" || scenario == "audit" || scenario == "outbox" {
				if scenario == "inbox" {
					m.triageInbox.AppendOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, agents.InboxReference) (int64, error) {
						return 0, fmt.Errorf("inbox unavailable")
					}
				}
				if scenario == "audit" {
					require.NoError(t, db.Exec("DROP TABLE audit_probe").Error)
				}
				if scenario == "outbox" {
					m.events = failPublisher{}
				}
				_, e := m.requestReleaseNoteHTTP(testContext(t, 2), &EditInput[ReleaseNoteRequest]{EntityID: "3", ID: pkg.String(), Key: "request", IfMatch: "\"1\"", Body: ReleaseNoteRequest{Note: "Request"}})
				require.Error(t, e)
				var n int64
				require.NoError(t, db.Table(m.names.ReleaseNoteDraft.String()).Count(&n).Error)
				require.Zero(t, n)
				require.NoError(t, db.Table("baseline_probe").Count(&n).Error)
				require.Zero(t, n)
				return
			}
			r := releaseNoteRequest(t, m, pkg)
			in := releaseNoteInput(t, m, pkg, r.Body.Data.ID)
			ctx := testContext(t, 8)
			switch scenario {
			case "stale":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("version", 2).Error)
				in.Body.PackageVersion = 2
			case "tamper":
				require.NoError(t, db.Table(m.names.ReleaseNoteDraft.String()).Where("id=?", r.Body.Data.ID).Update("source_snapshot", "{}").Error)
			case "fingerprint":
				in.Body.SourceFingerprint = releaseFingerprint("wrong")
			case "wrong_actor":
				ctx = testContext(t, 9)
			case "revoked":
				m.releaseDrafters.WriterOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return ErrForbidden }
			case "limit":
				for i := 0; i < 19; i++ {
					request := &EditInput[ReleaseNoteRequest]{EntityID: "3", ID: pkg.String(), Key: fmt.Sprint("req", i), IfMatch: "\"1\"", Body: ReleaseNoteRequest{Note: "Request"}}
					_, e := m.requestReleaseNoteHTTP(testContext(t, 2), request)
					require.NoError(t, e)
				}
				_, e := m.requestReleaseNoteHTTP(testContext(t, 2), &EditInput[ReleaseNoteRequest]{EntityID: "3", ID: pkg.String(), Key: "overflow", IfMatch: "\"1\"", Body: ReleaseNoteRequest{Note: "Request"}})
				require.Error(t, e)
				return
			}
			_, e := m.writeReleaseNoteHTTP(ctx, (*ReleaseNoteWriteInput)(in))
			require.Error(t, e)
		})
	}
}

func TestReleaseNoteRequestSourceFailuresLeaveNoDraftOrInbox(t *testing.T) {
	for _, scenario := range []string{"selection", "missing_selector", "missing_authority", "count", "insert", "audit", "inbox", "invalid_note"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, pkg := releaseNoteHarness(t)
			fault := errors.New("release note source failed")
			var before int64
			require.NoError(t, db.Table("audit_probe").Count(&before).Error)
			switch scenario {
			case "selection":
				m.releaseDrafters.SelectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 0, fault }
			case "missing_selector":
				m.releaseDrafters.SelectOn = nil
			case "missing_authority":
				m.releaseDrafters.AuthorizeOn = nil
			case "count":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:release-request", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ReleaseNoteDraft.String() {
						tx.AddError(fault)
					}
				}))
			case "insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:release-request", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ReleaseNoteDraft.String() {
						tx.AddError(fault)
					}
				}))
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "inbox":
				m.triageInbox.AppendOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, agents.InboxReference) (int64, error) {
					return 0, fault
				}
			}
			note := "Summarize verified package output"
			if scenario == "invalid_note" {
				note = " "
			}
			out, err := m.requestReleaseNoteHTTP(testContext(t, 2), &EditInput[ReleaseNoteRequest]{EntityID: "3", ID: pkg.String(), Key: "request-fault", IfMatch: `"1"`, Body: ReleaseNoteRequest{Note: note}})
			require.Error(t, err)
			require.Nil(t, out)
			require.NoError(t, db.Callback().Query().Remove("test:release-request"))
			require.NoError(t, db.Callback().Create().Remove("test:release-request"))
			var drafts, inbox, audits int64
			require.NoError(t, db.Table(m.names.ReleaseNoteDraft.String()).Count(&drafts).Error)
			require.Zero(t, drafts)
			require.NoError(t, db.Table("baseline_probe").Count(&inbox).Error)
			require.Zero(t, inbox)
			require.NoError(t, db.Table("audit_probe").Count(&audits).Error)
			require.Equal(t, before, audits)
		})
	}
}

func TestReleaseNoteWriteFailurePreservesRequestedSourceAndPackage(t *testing.T) {
	for _, scenario := range []string{"draft_read", "package_read", "update", "audit", "blank", "missing_draft", "source_archived"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, pkg := releaseNoteHarness(t)
			receipt := releaseNoteRequest(t, m, pkg)
			in := releaseNoteInput(t, m, pkg, receipt.Body.Data.ID)
			fault := errors.New("release draft write failed")
			switch scenario {
			case "draft_read", "package_read":
				table := m.names.ReleaseNoteDraft.String()
				if scenario == "package_read" {
					table = m.names.DeliveryPackage.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:release-write", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:release-write", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ReleaseNoteDraft.String() {
						tx.AddError(fault)
					}
				}))
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "blank":
				in.Body.Content = " "
			case "missing_draft":
				in.DraftID = "999"
			case "source_archived":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("archived_at", now()).Error)
			}
			out, err := m.writeReleaseNoteHTTP(testContext(t, 8), (*ReleaseNoteWriteInput)(in))
			require.Error(t, err)
			require.Nil(t, out)
			require.NoError(t, db.Callback().Query().Remove("test:release-write"))
			require.NoError(t, db.Callback().Update().Remove("test:release-write"))
			var row model.ReleaseNoteDraft
			require.NoError(t, db.Table(m.names.ReleaseNoteDraft.String()).Where("id=?", receipt.Body.Data.ID).Take(&row).Error)
			require.Equal(t, "REQUESTED", row.State)
			require.Nil(t, row.Content)
			require.Equal(t, 1, row.Version)
			var source model.DeliveryPackage
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&source).Error)
			require.Equal(t, "Manual notes", source.ReleaseNotes)
			require.Equal(t, 1, source.Version)
		})
	}
}
func TestReleaseNoteListsOnlySelectedCurrentAgentDrafts(t *testing.T) {
	m, db, pkg := releaseNoteHarness(t)
	receipt := releaseNoteRequest(t, m, pkg)
	human := m.principals.ActiveHumanByID
	m.principals.ActiveHumanByID = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		if id == 8 {
			return identity.Principal{}, false, nil
		}
		return human(ctx, tx, id)
	}
	var other model.ReleaseNoteDraft
	require.NoError(t, db.Table(m.names.ReleaseNoteDraft.String()).Where("id=?", receipt.Body.Data.ID).Take(&other).Error)
	other.ID++
	other.DrafterPrincipalID = 9
	require.NoError(t, db.Table(m.names.ReleaseNoteDraft.String()).Create(&other).Error)
	out, err := m.listReleaseNotesHTTP(testContext(t, 8), &ObjectInput{EntityID: "3", ID: pkg.String()})
	require.NoError(t, err)
	require.Len(t, out.Body.Data, 1)
	require.Equal(t, receipt.Body.Data.ID, out.Body.Data[0].ID)
	_, err = m.listReleaseNotesHTTP(testContext(t, 9), &ObjectInput{EntityID: "3", ID: pkg.String()})
	require.Error(t, err)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:release-list", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.ReleaseNoteDraft.String() {
			tx.AddError(ErrUnavailable)
		}
	}))
	out, err = m.listReleaseNotesHTTP(testContext(t, 8), &ObjectInput{EntityID: "3", ID: pkg.String()})
	require.Error(t, err)
	require.Nil(t, out)
}
