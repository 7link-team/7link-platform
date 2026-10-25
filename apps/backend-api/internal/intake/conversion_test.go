package intake

import (
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestReworkAdvancementPreservesEvidenceAndRollsBackFailedPublication(t *testing.T) {
	m, db, ctx, allowed, pub := changeSourceHarness(t)
	m.brief.explain = func(_ context.Context, _ *gorm.DB, in core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.Equal(t, "decision_request_changes", in.Permission)
		return authz.Explanation{Allowed: *allowed}, nil
	}
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "REWORK", "status": "RECEIVED", "raw_content": "frozen rejection"}).Error)
	advance := func(version int, complete bool) error {
		return db.Transaction(func(tx *gorm.DB) error {
			next, err := m.advanceReworkSourceOn(ctx, tx, 7, 20, 10, version, complete)
			if err == nil {
				require.Equal(t, version+1, next)
			}
			return err
		})
	}
	*allowed = false
	require.ErrorIs(t, advance(1, false), ErrForbidden)
	*allowed = true
	require.NoError(t, advance(1, false))
	require.ErrorIs(t, advance(1, true), ErrConflict)
	pub.fail = true
	require.Error(t, advance(2, true))
	var row model.Record
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&row).Error)
	require.Equal(t, 2, row.Version)
	require.Equal(t, "RECEIVED", row.Status)
	require.Nil(t, row.ResolvedAt)
	pub.fail = false
	require.NoError(t, advance(2, true))
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&row).Error)
	require.Equal(t, "CONVERTED", row.Status)
	require.Equal(t, "frozen rejection", *row.RawContent)
	require.Equal(t, "HUMAN", *row.ResolvedActorKind)
	require.Equal(t, "permission:decision_request_changes", *row.ResolvedAuthoritySource)
	require.NotNil(t, row.ResolvedAt)
	problem := errors.New("rework update unavailable")
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("rework_failure", func(tx *gorm.DB) { tx.AddError(problem) }))
	require.ErrorIs(t, advance(3, true), problem)
	require.NoError(t, db.Callback().Update().Remove("rework_failure"))
}

func TestProjectConversionMovesExactCardToDistinctProject(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "NEW", "status": "RECEIVED"}).Error)
	var card TriageSource
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		card, err = m.triageSourceOn(ctx, tx, 7, 20, 10, 1, triageDraft())
		return err
	}))
	for _, target := range []int64{0, 20} {
		require.ErrorIs(t, m.convertProjectSourceOn(ctx, nil, 7, 20, 10, 2, 1, card.Hash, guid.ID(target), 100), ErrInvalidArgument)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.convertProjectSourceOn(ctx, tx, 7, 20, 10, 2, 1, card.Hash, 21, 101)
	}))
	var row model.Record
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&row).Error)
	require.EqualValues(t, 21, *row.ProjectID)
	require.EqualValues(t, 101, *row.RequirementID)
	require.Equal(t, "NEW_PROJECT", *row.ConversionMode)
	require.Equal(t, "CONVERTED", row.Status)
}

func TestNewConversionPinsExactCardAndPreservesSource(t *testing.T) {
	m, db, ctx, allowed, pub := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "NEW", "status": "RECEIVED", "version": 1, "raw_content": "immutable"}).Error)
	var card TriageSource
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		card, err = m.triageSourceOn(ctx, tx, 7, 20, 10, 1, triageDraft())
		return err
	}))
	call := func(hash string) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.convertNewSourceOn(ctx, tx, 7, 20, 10, 2, 1, hash, 100) })
	}
	*allowed = false
	require.ErrorIs(t, call(card.Hash), ErrForbidden)
	*allowed = true
	require.ErrorIs(t, call("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"), ErrConflict)
	pub.fail = true
	require.Error(t, call(card.Hash))
	pub.fail = false
	source, err := m.readIntakeLedgerSourceOn(ctx, db, 7, 20, 10)
	require.Error(t, err, "reader requires a transaction")
	_ = source
	require.NoError(t, call(card.Hash))
	require.ErrorIs(t, call(card.Hash), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		source, err := m.readIntakeLedgerSourceOn(ctx, tx, 7, 20, 10)
		require.NoError(t, err)
		require.Equal(t, "CONVERTED", source.State)
		require.Equal(t, "immutable", *source.RawContent)
		require.Equal(t, card.Hash, source.Triage.Hash)
		return nil
	}))
}
