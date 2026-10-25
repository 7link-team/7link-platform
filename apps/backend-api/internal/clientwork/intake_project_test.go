package clientwork

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestIntakeProjectConversionExactSourceRollbackReplayAndRevocation(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	_, err := m.triageIntakeHTTP(testContext(t, 2), &EditInput[intake.TriageDraft]{EntityID: "3", ID: "50", IfMatch: `"1"`, Key: "project-triage", Body: intake.TriageDraft{Summary: "Export", RequirementDraft: "Export rows", Criteria: []string{"CSV"}, Size: "S"}})
	require.NoError(t, err)
	source, err := m.getIntakeLedgerHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: "50"})
	require.NoError(t, err)
	m.intakeProjects.Prepare = func(context.Context, *gorm.DB, guid.ID, delivery.IntakeProjectInput) ([]guid.ID, error) {
		return []guid.ID{2}, nil
	}
	calls := 0
	m.intakeProjects.CreateOn = func(_ context.Context, tx *gorm.DB, tenant, id guid.ID, in delivery.IntakeProjectInput, draft intake.TriageDraft, ids []guid.ID, cookie, proof string) (delivery.IntakeProjectResult, error) {
		calls++
		require.EqualValues(t, 50, id)
		require.Equal(t, "proof", proof)
		require.Equal(t, "session", cookie)
		require.Equal(t, []guid.ID{2}, ids)
		require.Equal(t, source.Body.Data.Triage.Draft, draft)
		return delivery.IntakeProjectResult{ProjectID: 100, RequirementID: 101, ChannelID: 102}, tx.Exec("INSERT INTO baseline_probe VALUES (101)").Error
	}
	in := &IntakeProjectConversionInput{EditInput: EditInput[IntakeProjectConversion]{EntityID: "3", ID: "50", IfMatch: `"2"`, Key: "project-convert", Body: IntakeProjectConversion{IntakeConversionBody: IntakeConversionBody{CardRevision: 1, ProposalHash: source.Body.Data.Triage.Hash, ReauthProof: "proof"}, Project: delivery.IntakeProjectInput{Name: "New project", Code: "NP"}}}, Cookie: "session"}
	convert := m.intakeSources.ConvertProjectOn
	m.intakeSources.ConvertProjectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, int, string, guid.ID, guid.ID) error {
		return errors.New("conversion outbox unavailable")
	}
	_, err = m.convertIntakeProjectHTTP(testContext(t, 2), in)
	require.Error(t, err)
	var n int64
	require.NoError(t, db.Table("baseline_probe").Count(&n).Error)
	require.Zero(t, n)
	m.intakeSources.ConvertProjectOn = convert
	result, err := m.convertIntakeProjectHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.EqualValues(t, 100, result.Body.Data.ID)
	var stored struct {
		Status, ConversionMode, RawContent string
		ProjectID, RequirementID           guid.ID
	}
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Take(&stored).Error)
	require.Equal(t, "CONVERTED", stored.Status)
	require.Equal(t, "NEW_PROJECT", stored.ConversionMode)
	require.EqualValues(t, 100, stored.ProjectID)
	require.EqualValues(t, 101, stored.RequirementID)
	require.Equal(t, "private original source", stored.RawContent)
	// Renewed credentials do not alter the idempotency hash or spend a second proof.
	in.Cookie = "renewed"
	in.Body.ReauthProof = "renewed"
	replay, err := m.convertIntakeProjectHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, 2, calls)
	projects.deny = true
	_, err = m.convertIntakeProjectHTTP(testContext(t, 2), in)
	require.Error(t, err)
}
