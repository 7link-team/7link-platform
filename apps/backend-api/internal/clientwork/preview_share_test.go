package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func previewShareFixture(t *testing.T, m *Module, db *gorm.DB) (Receipt, Receipt) {
	t.Helper()
	var preview, share Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		preview, e = m.createPreviewOn(testContext(t, 2), tx, 3, PreviewBody{Title: "Progress", SourceStage: "PROGRESS", Artifact: delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30}})
		if e != nil {
			return e
		}
		share, e = m.requestShareOn(testContext(t, 2), tx, 3, PreviewShareBody{PreviewID: preview.ID, RecipientID: 4, TTLHours: 24, Note: "Review this fixed version"})
		return e
	}))
	return preview, share
}

func TestPreviewShareRechecksUnsubmittedFileSource(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			m, db, projects, _ := harness(t)
			var preview, share Receipt
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				preview, e = m.createPreviewOn(testContext(t, 2), tx, 3, PreviewBody{Title: "Pre-result source", SourceStage: "PROGRESS", ProgressSource: &delivery.ClientSourceRef{WorkItemID: 11, FileID: 60, VersionID: 40, SHA256: strings.Repeat("a", 64)}})
				if e != nil {
					return e
				}
				share, e = m.requestShareOn(testContext(t, 2), tx, 3, PreviewShareBody{PreviewID: preview.ID, RecipientID: 4, TTLHours: 24, Note: "Review draft file bytes"})
				return e
			}))
			projects.stale = stale
			e := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.decideShareOn(testContext(t, 6), tx, 3, share.ID, 1, PreviewShareDecision{Decision: "APPROVE", Reason: "Reviewed actual source"})
				return e
			})
			if stale {
				require.Error(t, e)
			} else {
				require.NoError(t, e)
			}
			q := query.BindPreviewShare(db, m.names)
			stored, e := q.WithContext(testContext(t, 6)).Where(q.ID.Eq(share.ID)).First()
			require.NoError(t, e)
			if stale {
				require.Equal(t, "PENDING_APPROVAL", stored.State)
				require.Nil(t, stored.BindingID)
			} else {
				require.Equal(t, "ACTIVE", stored.State)
				require.NotNil(t, stored.BindingID)
			}
		})
	}
}

func TestPreviewShareReplacementConflictsWhenExpiryWins(t *testing.T) {
	m, db, _, _ := harness(t)
	preview, share := previewShareFixture(t, m, db)
	ctx := testContext(t, 2)
	q := query.BindPreviewShare(db, m.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).UpdateSimple(q.State.Value("ACTIVE"), q.ExpiresAt.Value(now().Add(-time.Minute)))
	require.NoError(t, err)
	before, err := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).First()
	require.NoError(t, err)
	fired := false
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("share_expiry_wins", func(tx *gorm.DB) {
		if fired || tx.Statement.Table != m.names.PreviewShare.String() {
			return
		}
		fired = true
		winner := query.BindPreviewShare(tx.Session(&gorm.Session{NewDB: true}), m.names)
		_, err := winner.WithContext(ctx).Where(winner.ID.Eq(share.ID)).UpdateSimple(winner.State.Value("EXPIRED"), winner.ActiveSlot.Null(), winner.Version.Add(1))
		_ = tx.AddError(err)
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove("share_expiry_wins")) })
	events := 0
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { events++; return nil }
	err = db.Transaction(func(tx *gorm.DB) error {
		_, err := m.requestShareOn(ctx, tx, 3, PreviewShareBody{PreviewID: preview.ID, RecipientID: 4, TTLHours: 1, Note: "Replacement"})
		return err
	})
	require.True(t, fired)
	require.ErrorIs(t, err, ErrConflict)
	require.Zero(t, events)
	after, err := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).First()
	require.NoError(t, err)
	require.Equal(t, before, after)
	rows, err := q.WithContext(ctx).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestPreviewShareDecisionCASRejectsConcurrentChanges(t *testing.T) {
	for _, decision := range []string{"APPROVE_GRANT", "APPROVE_FINAL", "REJECT", "REVOKE", "REVOKE_ACTIVE"} {
		for _, mutation := range []string{"version", "state"} {
			t.Run(decision+"/"+mutation, func(t *testing.T) {
				m, db, _, _ := harness(t)
				_, share := previewShareFixture(t, m, db)
				ctx := testContext(t, 6)
				if decision == "REVOKE_ACTIVE" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						_, err := m.decideShareOn(ctx, tx, 3, share.ID, 1, PreviewShareDecision{Decision: "APPROVE", Reason: "Reviewed"})
						return err
					}))
				}
				q := query.BindPreviewShare(db, m.names)
				before, err := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).First()
				require.NoError(t, err)
				updates, target, fired := 0, 1, false
				if decision == "APPROVE_FINAL" {
					target = 2
				}
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("share_decision_race", func(tx *gorm.DB) {
					if fired || tx.Statement.Table != m.names.PreviewShare.String() {
						return
					}
					updates++
					if updates != target {
						return
					}
					fired = true
					winner := query.BindPreviewShare(tx.Session(&gorm.Session{NewDB: true}), m.names)
					if mutation == "version" {
						_, err = winner.WithContext(ctx).Where(winner.ID.Eq(share.ID)).UpdateSimple(winner.Version.Add(1))
					} else {
						_, err = winner.WithContext(ctx).Where(winner.ID.Eq(share.ID)).UpdateSimple(winner.State.Value("EXPIRED"))
					}
					_ = tx.AddError(err)
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove("share_decision_race")) })
				events, pins := 0, 0
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { events++; return nil }
				m.files.PinOn = func(_ context.Context, tx *gorm.DB, in files.ResourceBindingInput) (files.PinnedResource, error) {
					pins++
					return files.PinnedResource{BindingID: in.ID}, tx.Exec("INSERT INTO baseline_probe VALUES (?)", in.ID).Error
				}
				action := decision
				if decision == "APPROVE_GRANT" || decision == "APPROVE_FINAL" {
					action = "APPROVE"
				} else if decision == "REVOKE_ACTIVE" {
					action = "REVOKE"
				}
				err = db.Transaction(func(tx *gorm.DB) error {
					_, err := m.decideShareOn(ctx, tx, 3, share.ID, before.Version, PreviewShareDecision{Decision: action, Reason: "Reviewed"})
					return err
				})
				require.True(t, fired)
				require.ErrorIs(t, err, ErrConflict)
				require.Zero(t, events)
				if decision == "APPROVE_FINAL" {
					require.Equal(t, 1, pins)
				} else {
					require.Zero(t, pins)
				}
				after, err := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).First()
				require.NoError(t, err)
				require.Equal(t, before, after)
				var bindings int64
				require.NoError(t, db.Table("baseline_probe").Count(&bindings).Error)
				require.Zero(t, bindings, "a final CAS conflict also rolls back the pin")
			})
		}
	}
}

func TestPreviewShareApprovalExpiryPrivacyAndCurrentMembership(t *testing.T) {
	m, db, _, clients := harness(t)
	preview, share := previewShareFixture(t, m, db)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.decideShareOn(testContext(t, 2), tx, 3, share.ID, 1, PreviewShareDecision{Decision: "APPROVE", Reason: "Ready"})
		return e
	}), ErrForbidden)
	var approved Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		approved, e = m.decideShareOn(testContext(t, 6), tx, 3, share.ID, 1, PreviewShareDecision{Decision: "APPROVE", Reason: "Checked audience"})
		return e
	}))
	require.Equal(t, "ACTIVE", approved.State)
	input := &ObjectInput{EntityID: "3", ID: share.ID.String()}
	view, e := m.getShareHTTP(testContext(t, 4), input)
	require.NoError(t, e)
	require.Equal(t, "PROGRESS", view.Body.Data.Preview.SourceStage)
	data, e := json.Marshal(view.Body.Data)
	require.NoError(t, e)
	for _, field := range []string{"work_item_id", "result_id", "evidence_id", "requested_by", "decided_by", "recipient_id", "binding_id", "token"} {
		require.NotContains(t, string(data), field)
	}
	_, e = m.getShareHTTP(testContext(t, 5), input)
	require.Error(t, e)
	_, e = m.get_previewHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: preview.ID.String()})
	require.Error(t, e, "a share must not permanently publish its source preview")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.shareResourceOn(testContext(t, 4), tx, share.ID, files.ResourceOwner{Scope: "entity", ID: 3})
	}))
	clients.revoked = true
	_, e = m.getShareHTTP(testContext(t, 4), input)
	require.Error(t, e)
	clients.revoked = false
	q := query.BindPreviewShare(db, m.names)
	_, e = q.WithContext(testContext(t, 2)).Where(q.ID.Eq(share.ID)).Update(q.ExpiresAt, now().Add(-time.Second))
	require.NoError(t, e)
	_, e = m.getShareHTTP(testContext(t, 4), input)
	require.Error(t, e)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.authorizedShareOn(testContext(t, 4), tx, 3, share.ID); return e }), ErrShareEnded)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.requestShareOn(testContext(t, 2), tx, 3, PreviewShareBody{PreviewID: preview.ID, RecipientID: 4, TTLHours: 1, Note: "New review"})
		return e
	}))
	old, e := q.WithContext(testContext(t, 2)).Where(q.ID.Eq(share.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "EXPIRED", old.State)
	require.Nil(t, old.ActiveSlot)
}
func TestPreviewShareRejectRevokeVersionAndRollback(t *testing.T) {
	for _, scenario := range []string{"REJECT", "REVOKE", "audit", "pin", "version", "invalid", "revoked_recipient"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _, clients := harness(t)
			_, share := previewShareFixture(t, m, db)
			q := query.BindPreviewShare(db, m.names)
			before, e := q.WithContext(testContext(t, 2)).Where(q.ID.Eq(share.ID)).First()
			require.NoError(t, e)
			failure := errors.New("boundary failed")
			decision := "APPROVE"
			version := 1
			switch scenario {
			case "REJECT", "REVOKE":
				decision = scenario
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "pin":
				m.files.PinOn = func(context.Context, *gorm.DB, files.ResourceBindingInput) (files.PinnedResource, error) {
					return files.PinnedResource{}, failure
				}
			case "version":
				version = 2
			case "invalid":
				decision = "UNKNOWN"
			case "revoked_recipient":
				clients.revoked = true
			}
			e = db.Transaction(func(tx *gorm.DB) error {
				_, e := m.decideShareOn(testContext(t, 6), tx, 3, share.ID, version, PreviewShareDecision{Decision: decision, Reason: "Reviewed"})
				return e
			})
			after, readErr := q.WithContext(testContext(t, 2)).Where(q.ID.Eq(share.ID)).First()
			require.NoError(t, readErr)
			if scenario == "REJECT" || scenario == "REVOKE" {
				require.NoError(t, e)
				require.Nil(t, after.ActiveSlot)
				require.NotEqual(t, before.State, after.State)
			} else {
				require.Error(t, e)
				require.Equal(t, before, after)
			}
		})
	}
}
func TestPreviewShareRequestValidationAndOneLiveShare(t *testing.T) {
	m, db, _, _ := harness(t)
	preview, _ := previewShareFixture(t, m, db)
	for _, in := range []PreviewShareBody{
		{PreviewID: preview.ID, RecipientID: 4, TTLHours: 24, Note: "Duplicate"},
		{PreviewID: preview.ID, RecipientID: 4, TTLHours: 169, Note: "Too long"},
		{PreviewID: preview.ID, RecipientID: 2, TTLHours: 24, Note: "Not invited"},
	} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.requestShareOn(testContext(t, 2), tx, 3, in); return e }))
	}
	// Human approval authority is required again for replay, before receipts.
	ctx := testContext(t, 6)
	input := &EditInput[PreviewShareDecision]{EntityID: "3", ID: guid.ID(999).String(), Key: "absent", IfMatch: "\"1\"", Body: PreviewShareDecision{Decision: "APPROVE", Reason: "Reviewed"}}
	_, e := m.decideShareHTTP(ctx, input)
	require.Error(t, e)
}
