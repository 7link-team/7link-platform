package fleet

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnsembleNoticesUseActualCurrentRequestAndHumanAuthority(t *testing.T) {
	m, db, ctx, _, _ := ensembleHarness(t)
	var plan EnsembleView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		plan, e = m.CreateEnsembleOn(ctx, tx, 7, 9, EnsembleCreate{20, []guid.ID{11, 12}, "Actual historical alternatives"})
		return e
	}))
	read := func(pending bool) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			f, found, e := m.ensembleDecisionFactOn(ctx, tx, 7, plan.ID)
			require.NoError(t, e)
			require.True(t, found)
			require.Equal(t, pending, f.ApprovalPending)
			_, found, e = m.ensembleDecisionFactOn(ctx, tx, 8, plan.ID)
			require.NoError(t, e)
			require.False(t, found)
			return nil
		}))
	}
	read(false)
	change := func(action string, version int) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.ChangeEnsembleOn(ctx, tx, 7, 9, plan.ID, EnsembleDecision{Action: action, Version: version, Reason: "Human review actual comparison"})
			return e
		}))
	}
	change("REQUEST_APPROVAL", 1)
	read(true)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		yes, e := m.ensembleDecisionEligibleOn(ctx, tx, 7, plan.ID, 101)
		require.NoError(t, e)
		require.True(t, yes)
		yes, e = m.ensembleDecisionEligibleOn(ctx, tx, 7, plan.ID, 102)
		require.NoError(t, e)
		require.False(t, yes)
		return nil
	}))
	change("APPROVE", 2)
	read(false)
	_, _, e := m.ensembleDecisionFactOn(ctx, db, 7, plan.ID)
	require.ErrorIs(t, e, ErrInvalidArgument)
}

func TestEnsemblePendingCandidatesReflectCurrentDomainState(t *testing.T) {
	m, db, ctx, _, _ := ensembleHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		p, err := m.CreateEnsembleOn(ctx, tx, 7, 9, EnsembleCreate{20, []guid.ID{11, 12}, "Historical comparison candidates"})
		require.NoError(t, err)
		rows, err := m.ensemblePendingDecisionsOn(ctx, tx, 7, 9)
		require.NoError(t, err)
		require.Empty(t, rows)
		_, err = m.ChangeEnsembleOn(ctx, tx, 7, 9, p.ID, EnsembleDecision{Action: "REQUEST_APPROVAL", Version: 1, Reason: "Request actual Human approval"})
		require.NoError(t, err)
		rows, err = m.ensemblePendingDecisionsOn(ctx, tx, 7, 9)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, "ENSEMBLE_APPROVAL", rows[0].Kind)
		rows, err = m.ensemblePendingDecisionsOn(ctx, tx, 8, 9)
		require.NoError(t, err)
		require.Empty(t, rows)
		rows, err = m.ensemblePendingDecisionsOn(ctx, tx, 7, 10)
		require.NoError(t, err)
		require.Empty(t, rows)
		_, err = m.ChangeEnsembleOn(ctx, tx, 7, 9, p.ID, EnsembleDecision{Action: "APPROVE", Version: 2, Reason: "Actual Human approves comparison"})
		require.NoError(t, err)
		rows, err = m.ensemblePendingDecisionsOn(ctx, tx, 7, 0)
		require.NoError(t, err)
		require.Empty(t, rows)
		return nil
	}))
	_, err := m.ensemblePendingDecisionsOn(ctx, db, 7, 9)
	require.ErrorIs(t, err, ErrInvalidArgument)
}
