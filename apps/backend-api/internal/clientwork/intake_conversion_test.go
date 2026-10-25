package clientwork

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestNewIntakeConversionRollsBackCanonicalDraftWhenSourceCannotAdvance(t *testing.T) {
	for _, stage := range []string{"denied", "missing_provider", "read", "draft", "source_advance"} {
		t.Run(stage, func(t *testing.T) {
			m, db, projects, _ := clarificationHarness(t)
			ctx := testContext(t, 2)
			_, err := m.triageIntakeHTTP(ctx, &EditInput[intake.TriageDraft]{EntityID: "3", ID: "50", IfMatch: `"1"`, Key: "triage-failure-source", Body: intake.TriageDraft{Summary: "Export", RequirementDraft: "Export rows", Criteria: []string{"CSV"}, Size: "S"}})
			require.NoError(t, err)
			source, err := m.getIntakeLedgerHTTP(ctx, &ObjectInput{EntityID: "3", ID: "50"})
			require.NoError(t, err)
			failure := errors.New("canonical intake conversion unavailable")
			m.intakeAppend.NewRequirementOn = func(_ context.Context, tx *gorm.DB, tenant, project, id guid.ID, draft intake.TriageDraft) (guid.ID, error) {
				if stage == "draft" {
					return 0, failure
				}
				return 100, tx.Exec("INSERT INTO baseline_probe VALUES (100)").Error
			}
			switch stage {
			case "denied":
				projects.deny = true
			case "missing_provider":
				m.intakeAppend.NewRequirementOn = nil
			case "read":
				m.intakeSources.SourceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.IntakeLedgerSource, error) {
					return intake.IntakeLedgerSource{}, failure
				}
			case "source_advance":
				m.intakeSources.ConvertNewOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, int, string, guid.ID) error {
					return failure
				}
			}
			before := collaborationSnapshot(t, m, db)
			err = db.Transaction(func(tx *gorm.DB) error {
				_, err := m.convertNewIntakeOn(ctx, tx, 3, 50, 2, IntakeNewConversion{CardRevision: 1, ProposalHash: source.Body.Data.Triage.Hash})
				return err
			})
			require.Error(t, err)
			require.Equal(t, before, collaborationSnapshot(t, m, db))
			var state string
			require.NoError(t, db.Table("g_intake_records").Where("id=50").Select("status").Scan(&state).Error)
			require.Equal(t, "TRIAGED", state)
		})
	}
}

func TestDelegatedNewPreviewRejectsInvalidPathIDs(t *testing.T) {
	for _, in := range []DelegatedNewCardInput{{EntityID: "bad", ID: "50"}, {EntityID: "3", ID: "bad"}, {EntityID: "3", ID: "0"}} {
		_, err := (&Module{}).getDelegatedNewCardHTTP(context.Background(), &in)
		var status huma.StatusError
		require.ErrorAs(t, err, &status)
		require.Equal(t, 422, status.GetStatus())
	}
}

func TestNewIntakeConversionRejectsStaleCardAndReplaysCanonicalDraft(t *testing.T) {
	m, _, projects, _ := clarificationHarness(t)
	triaged, err := m.triageIntakeHTTP(testContext(t, 2), &EditInput[intake.TriageDraft]{EntityID: "3", ID: "50", IfMatch: `"1"`, Key: "triage-new", Body: intake.TriageDraft{Summary: "Export", RequirementDraft: "Export all rows", Criteria: []string{"CSV contains all rows"}, Size: "S"}})
	require.NoError(t, err)
	source, err := m.getIntakeLedgerHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: "50"})
	require.NoError(t, err)
	calls := 0
	m.intakeAppend.NewRequirementOn = func(_ context.Context, tx *gorm.DB, tenant, project, id guid.ID, draft intake.TriageDraft) (guid.ID, error) {
		calls++
		require.Equal(t, guid.ID(50), id)
		require.Equal(t, source.Body.Data.Triage.Draft, draft)
		return 100, tx.Exec("INSERT INTO baseline_probe VALUES (100)").Error
	}
	in := &EditInput[IntakeNewConversion]{EntityID: "3", ID: "50", IfMatch: `"2"`, Key: "convert-new", Body: IntakeNewConversion{CardRevision: 1, ProposalHash: source.Body.Data.Triage.Hash}}
	stale := *in
	stale.Key = "stale-card"
	stale.Body.CardRevision = 2
	_, err = m.convertNewIntakeHTTP(testContext(t, 2), &stale)
	require.Error(t, err)
	require.Zero(t, calls)
	result, err := m.convertNewIntakeHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, guid.ID(100), result.Body.Data.ID)
	require.Equal(t, "DRAFT", result.Body.Data.State)
	replay, err := m.convertNewIntakeHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, result.Body.Data, replay.Body.Data)
	require.Equal(t, 1, calls)
	projects.deny = true
	_, err = m.convertNewIntakeHTTP(testContext(t, 2), in)
	require.Error(t, err)
	_ = triaged
}

func TestDelegatedNewCommandReauthorizesBeforeReplayAndRollsBack(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	// Ordinary Human project adapter is forbidden: delegated conversion must use
	// exactly its canonical authority provider, not silently call scope_change.
	projects.deny = true
	allowed, failWrite := true, false
	authorizationCalls, creationCalls := 0, 0
	failure := errors.New("atomic conversion failure")
	m.delegatedConversion = delivery.IntakeDelegatedConversion{
		AuthorizeOn: func(_ context.Context, tx *gorm.DB, tenant, project, source guid.ID) error {
			authorizationCalls++
			require.EqualValues(t, 50, source)
			if !allowed {
				return delivery.ErrProjectNonDelegable
			}
			return nil
		},
		CreateOn: func(_ context.Context, tx *gorm.DB, tenant, project, source guid.ID, version, revision int, hash string) (guid.ID, error) {
			creationCalls++
			require.Equal(t, 2, version)
			require.Equal(t, 1, revision)
			if err := tx.Exec("INSERT INTO baseline_probe VALUES (100)").Error; err != nil {
				return 0, err
			}
			if failWrite {
				return 0, failure
			}
			return 100, nil
		},
	}
	in := &DelegatedNewInput{EditInput[IntakeNewConversion]{EntityID: "3", ID: "50", IfMatch: `"2"`, Key: "delegated-new", Body: IntakeNewConversion{CardRevision: 1, ProposalHash: strings.Repeat("a", 64)}}}
	failWrite = true
	_, err := m.convertDelegatedNewHTTP(testContext(t, 2), in)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
	require.Zero(t, count)
	failWrite = false
	first, err := m.convertDelegatedNewHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "DRAFT", first.Body.Data.State)
	replay, err := m.convertDelegatedNewHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	require.Equal(t, 2, creationCalls, "failed effect must not leave an idempotency receipt")
	allowed = false
	_, err = m.convertDelegatedNewHTTP(testContext(t, 2), in)
	require.Error(t, err, "current delegation is checked even for a previously successful replay")
	require.Equal(t, 4, authorizationCalls)
	require.Equal(t, 2, creationCalls)
}
