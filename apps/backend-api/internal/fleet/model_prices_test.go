package fleet

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestModelPricesVersionReplayDeletionUnknownAndAtomicEffects(t *testing.T) {
	f := inspectionHarness(t)
	write := func(version int, in *ModelPriceWrite) (ModelPriceView, error) {
		var out ModelPriceView
		err := f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = f.m.WriteModelPriceOn(f.ctx, tx, 7, "CODEX", "proxy", version, in)
			return e
		})
		return out, err
	}
	in := &ModelPriceWrite{Input: 9007199254740993, Output: 17, CacheRead: 3, CacheWrite: 7}
	first, err := write(0, in)
	require.NoError(t, err)
	require.Equal(t, 1, first.Version)
	var effects int64
	require.NoError(t, f.db.Table("test_fleet_effects").Count(&effects).Error)
	replay, err := write(0, in)
	require.NoError(t, err)
	require.Equal(t, first, replay)
	var after int64
	require.NoError(t, f.db.Table("test_fleet_effects").Count(&after).Error)
	require.Equal(t, effects, after)
	_, err = write(0, &ModelPriceWrite{Input: 1})
	require.ErrorIs(t, err, ErrVersionConflict)
	f.publisher.fail = true
	_, err = write(1, &ModelPriceWrite{Input: 5})
	require.Error(t, err)
	f.publisher.fail = false
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		page, e := f.m.ModelPricesOn(f.ctx, tx, 7, 0, 1)
		require.NoError(t, e)
		require.Equal(t, first, page.Items[0])
		return e
	}))
	deleted, err := write(1, nil)
	require.NoError(t, err)
	require.Equal(t, 2, deleted.Version)
	_, err = write(1, nil)
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		price, e := f.m.ModelPriceOn(f.ctx, tx, 7, "CODEX", "proxy")
		require.Nil(t, price)
		return e
	}))
	revived, err := write(0, &ModelPriceWrite{})
	require.NoError(t, err)
	require.Equal(t, 3, revived.Version)
	_, err = write(1, nil)
	require.ErrorIs(t, err, ErrVersionConflict)
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = write(0, &ModelPriceWrite{})
	require.ErrorIs(t, err, ErrMachineForbidden)
}
func TestModelPricesRejectMalformedAndFailedTransactions(t *testing.T) {
	f := inspectionHarness(t)
	require.ErrorIs(t, func() error {
		_, e := f.m.WriteModelPriceOn(f.ctx, f.db, 7, "CODEX", "x", 0, &ModelPriceWrite{})
		return e
	}(), ErrInvalidArgument)
	for _, in := range []ModelPriceWrite{{Input: -1}, {Output: -1}, {CacheRead: -1}, {CacheWrite: -1}} {
		require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.WriteModelPriceOn(f.ctx, tx, 7, "CODEX", "x", 0, &in); return e }), ErrInvalidArgument)
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.WriteModelPriceOn(f.ctx, tx, 7, "CODEX", "x", 0, &ModelPriceWrite{})
		return e
	}))
	sentinel := errors.New("rollback")
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.WriteModelPriceOn(f.ctx, tx, 7, "CODEX", "x", 1, nil)
		require.NoError(t, e)
		return sentinel
	}), sentinel)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		price, e := f.m.ModelPriceOn(f.ctx, tx, 7, "CODEX", "x")
		require.NotNil(t, price)
		return e
	}))
}
