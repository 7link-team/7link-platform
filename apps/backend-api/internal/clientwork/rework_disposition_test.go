package clientwork

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestReworkNewRequirementGroupsExactSubsetAndRollsBackEveryFailure(t *testing.T) {
	for _, stage := range []string{"success", "missing_draft", "missing_provider", "provider", "source", "stale", "query", "create", "advance", "ids"} {
		t.Run(stage, func(t *testing.T) {
			m, db, _, _ := harness(t)
			failure := errors.New("rework boundary unavailable")
			hash := strings.Repeat("a", 64)
			items := []intake.ReworkItem{{ItemID: "first"}, {ItemID: "second"}}
			m.reworkSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.ReworkSourceView, bool, error) {
				if stage == "source" {
					return intake.ReworkSourceView{}, false, failure
				}
				version := 1
				if stage == "stale" {
					version = 2
				}
				return intake.ReworkSourceView{ID: 50, Version: version, State: "RECEIVED", ContentHash: hash, Rejection: intake.ReworkDraft{Items: items}}, true, nil
			}
			m.reworkSources.AdvanceOn = func(_ context.Context, tx *gorm.DB, tenant, project, id guid.ID, v int, complete bool) (int, error) {
				require.True(t, complete)
				require.EqualValues(t, 50, id)
				if stage == "advance" {
					return 0, failure
				}
				return 2, nil
			}
			calls := 0
			m.reworkDisposition.NewRequirementOn = func(_ context.Context, tx *gorm.DB, tenant, project, id guid.ID, draft intake.TriageDraft) (guid.ID, error) {
				calls++
				require.EqualValues(t, 50, id)
				require.Equal(t, "Client scope", draft.Summary)
				if stage == "provider" {
					return 0, failure
				}
				return 90, tx.Exec("INSERT INTO baseline_probe VALUES (90)").Error
			}
			in := &ReworkDispositionInput{EditInput: EditInput[ReworkDispositionBody]{Body: ReworkDispositionBody{ReceiptID: 71, ContentHash: hash, Draft: &intake.TriageDraft{Summary: "Client scope"}, Items: []ReworkDispositionItem{{ItemID: "first", Mode: "NEW_REQUIREMENT", Reason: "Separate scope", ClientMessage: "New scope recorded"}, {ItemID: "second", Mode: "NEW_REQUIREMENT", Reason: "Same scope", ClientMessage: "Grouped with first"}}}}}
			switch stage {
			case "missing_draft":
				in.Body.Draft = nil
			case "missing_provider":
				m.reworkDisposition.NewRequirementOn = nil
			case "ids":
				m.ids = zeroIDs{}
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("regression_rework", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ReworkDisposition.String() {
						tx.AddError(failure)
					}
				}))
			}
			// Install read failures only after the snapshot to keep its oracle independent.
			before := collaborationSnapshot(t, m, db)
			if stage == "query" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_rework", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ReworkDisposition.String() {
						tx.AddError(failure)
					}
				}))
			}
			var receipt Receipt
			err := db.Transaction(func(tx *gorm.DB) error {
				var err error
				receipt, err = m.disposeReworkOn(testContext(t, 2), tx, 3, 50, 1, in)
				return err
			})
			require.NoError(t, db.Callback().Query().Remove("regression_rework"))
			require.NoError(t, db.Callback().Create().Remove("regression_rework"))
			if stage != "success" {
				require.Error(t, err)
				require.Equal(t, before, collaborationSnapshot(t, m, db))
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, calls, "one canonical requirement for the entire selected subset")
			require.Equal(t, "CONVERTED", receipt.State)
			var rows []struct{ TargetID guid.ID }
			require.NoError(t, db.Table(m.names.ReworkDisposition.String()).Find(&rows).Error)
			require.Len(t, rows, 2)
			for _, row := range rows {
				require.EqualValues(t, 90, row.TargetID)
			}
		})
	}
}

func TestReworkDispositionMixedSubsetRollbackReplayAndSupplement(t *testing.T) {
	m, db, projects, _ := harness(t)
	realIntakeSources(t, m, db)
	require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
	var source intake.ReworkSourceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		source, _, err = m.reworkSources.CreateOn(testContext(t, 4), tx, 1, 3, intake.ReworkDraft{PackageID: 70, ReceiptID: 71, RecipientID: 4, ContentHash: strings.Repeat("a", 64), Items: []intake.ReworkItem{{ItemID: strings.Repeat("a", 64), WorkItemID: 11, ResultID: 20, EvidenceID: 30, RequirementID: 10, SourceIntakeID: 50, Comment: "broken export"}, {ItemID: strings.Repeat("b", 64), WorkItemID: 12, ResultID: 21, EvidenceID: 31, RequirementID: 10, SourceIntakeID: 50, Comment: "broken import"}}})
		return err
	}))
	require.Positive(t, source.ID)
	ledger, err := m.getIntakeLedgerHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: source.ID.String()})
	require.NoError(t, err)
	require.NotNil(t, ledger.Body.Data.Rework)
	require.Equal(t, source, *ledger.Body.Data.Rework, "internal source uses the actual immutable receipt and rejected items")

	m.reworkDisposition.AuthorizeOn = func(ctx context.Context, _ *gorm.DB, tenant, project guid.ID) error {
		if tenant != 1 || project != 3 {
			return ErrForbidden
		}
		return nil
	}
	m.reworkDisposition.ReopenOn = func(_ context.Context, tx *gorm.DB, _, _ guid.ID, fact intake.ReworkItem, v int, reason string) error {
		require.Equal(t, strings.Repeat("a", 64), fact.ItemID)
		require.Equal(t, 7, v)
		return tx.Exec("INSERT INTO baseline_probe VALUES (11)").Error
	}
	request := &ReworkDispositionInput{EditInput: EditInput[ReworkDispositionBody]{EntityID: "3", ID: source.ID.String(), IfMatch: `"1"`, Key: "dispose-first", Body: ReworkDispositionBody{ReceiptID: 71, ContentHash: source.ContentHash, Items: []ReworkDispositionItem{{ItemID: strings.Repeat("a", 64), Mode: "REOPEN_WORK_ITEMS", WorkVersion: 7, Reason: "reproduce the export bug", ClientMessage: "We will repair the export"}}}}}
	bad := *request
	bad.Body = request.Body
	bad.Body.Items = []ReworkDispositionItem{{ItemID: "accepted-item", Mode: "DECLINE", Reason: "not rejected", ClientMessage: "not applicable"}}
	bad.Key = "bad-subset"
	_, err = m.reworkDispositionHTTP(testContext(t, 2), &bad)
	require.Error(t, err)
	old := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit down") }
	_, err = m.reworkDispositionHTTP(testContext(t, 2), request)
	require.Error(t, err)
	var n int64
	require.NoError(t, db.Table("baseline_probe").Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, db.Table(m.names.ReworkDisposition.String()).Count(&n).Error)
	require.Zero(t, n)
	m.audit = old
	got, err := m.reworkDispositionHTTP(testContext(t, 2), request)
	require.NoError(t, err)
	require.Equal(t, 2, got.Body.Data.Version)
	require.Equal(t, "RECEIVED", got.Body.Data.State)
	replay, err := m.reworkDispositionHTTP(testContext(t, 2), request)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	projects.deny = true
	_, err = m.reworkDispositionHTTP(testContext(t, 2), request)
	require.Error(t, err)
	projects.deny = false
	request.Key = "dispose-rest"
	request.IfMatch = `"2"`
	request.Body.Items = []ReworkDispositionItem{{ItemID: strings.Repeat("b", 64), Mode: "DECLINE", Reason: "outside agreed scope", ClientMessage: "Please discuss a separate scope"}}
	got, err = m.reworkDispositionHTTP(testContext(t, 2), request)
	require.NoError(t, err)
	require.Equal(t, "CONVERTED", got.Body.Data.State)
	require.NoError(t, db.Table(m.names.ReworkDisposition.String()).Count(&n).Error)
	require.EqualValues(t, 2, n)
	request.Key = "duplicate-decline"
	request.IfMatch = `"3"`
	_, err = m.reworkDispositionHTTP(testContext(t, 2), request)
	require.Error(t, err)
	// A later supplemental APPEND remains available on an already converted source.
	request.Key = "supplement"
	request.Body.Items = []ReworkDispositionItem{{ItemID: strings.Repeat("a", 64), Mode: "APPEND_TO_REQUIREMENT", Reason: "include the clarified contract", ClientMessage: "We updated the requirement"}}
	request.Body.RequirementID = 10
	request.Body.RequirementVersion = 1
	request.Body.Append = &delivery.ClientScopeChange{Summary: "Clarify the export contract", BaseVersionNo: 1, ReauthProof: "fresh"}
	got, err = m.reworkDispositionHTTP(testContext(t, 2), request)
	require.NoError(t, err)
	require.Equal(t, 4, got.Body.Data.Version)
	require.Equal(t, "CONVERTED", got.Body.Data.State)
}

func TestReworkDispositionGroupsOnlyConsistentAcceptedResults(t *testing.T) {
	m, db, _, _ := harness(t)
	items := []intake.ReworkItem{{ItemID: "first", WorkItemID: 11, ResultID: 20, EvidenceID: 30, RequirementID: 10}, {ItemID: "second", WorkItemID: 11, ResultID: 21, EvidenceID: 31, RequirementID: 10}}
	m.reworkSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.ReworkSourceView, bool, error) {
		return intake.ReworkSourceView{ID: 50, Version: 1, State: "RECEIVED", ContentHash: strings.Repeat("a", 64), Rejection: intake.ReworkDraft{Items: items}}, true, nil
	}
	m.reworkSources.AdvanceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, bool) (int, error) { return 2, nil }
	calls := 0
	m.reworkDisposition.ReopenOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, intake.ReworkItem, int, string) error {
		calls++
		return nil
	}
	in := &ReworkDispositionInput{EditInput: EditInput[ReworkDispositionBody]{Body: ReworkDispositionBody{ReceiptID: 71, ContentHash: strings.Repeat("a", 64), Items: []ReworkDispositionItem{{ItemID: "first", Mode: "REOPEN_WORK_ITEMS", WorkVersion: 7, Reason: "Repair", ClientMessage: "Repair scheduled"}, {ItemID: "second", Mode: "REOPEN_WORK_ITEMS", WorkVersion: 7, Reason: "Repair", ClientMessage: "Repair scheduled"}}}}}
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error { _, err := m.disposeReworkOn(testContext(t, 2), tx, 3, 50, 1, in); return err })
	}
	require.ErrorIs(t, run(), ErrInvalid, "one work cannot group evidence from different accepted results")
	require.Zero(t, calls, "validate the entire subset before reopening")
	items[1].ResultID, items[1].RequirementID = 20, 99
	require.ErrorIs(t, run(), ErrInvalid)
	require.Zero(t, calls)
	items[1].RequirementID = 10
	require.NoError(t, run())
	require.Equal(t, 1, calls, "two rejected artifacts from one accepted result reopen the work once")
}
