package intake

import (
	"7link.com/platform/internal/intake/model"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestSuggestionDiscoveryPaginatesEligibleSourcesWithoutContent(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"status": "RECEIVED", "raw_content": "private source"}).Error)
	var first model.Record
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&first).Error)
	next := first
	next.ID, next.RefNo, next.ReplyToken = 11, 2, "discovery-11"
	require.NoError(t, db.Table(m.names.Records.String()).Create(&next).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, err := m.suggestionSourcesOn(ctx, tx, 7, 20, 0, 1)
		require.NoError(t, err)
		require.True(t, page.HasMore)
		require.Len(t, page.Items, 1)
		require.EqualValues(t, 11, page.Items[0].ID)
		require.Nil(t, page.Items[0].Content)
		tail, err := m.suggestionSourcesOn(ctx, tx, 7, 20, page.Items[0].ID, 1)
		require.NoError(t, err)
		require.False(t, tail.HasMore)
		require.Len(t, tail.Items, 1)
		require.EqualValues(t, 10, tail.Items[0].ID)
		require.Nil(t, tail.Items[0].Content)
		return nil
	}))
}

func TestSuggestionProjectResolutionRejectsForeignTenantAndUnassignedSource(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	_, err := m.suggestionProjectOn(ctx, db, 7, 10)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.suggestionProjectOn(ctx, nil, 7, 10)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		project, err := m.suggestionProjectOn(ctx, tx, 7, 10)
		require.NoError(t, err)
		require.EqualValues(t, 20, project)
		_, err = m.suggestionProjectOn(ctx, tx, 8, 10)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = m.suggestionProjectOn(ctx, tx, 7, 999)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		return nil
	}))
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("project_id", nil).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		project, err := m.suggestionProjectOn(ctx, tx, 7, 10)
		require.Zero(t, project)
		return err
	}), ErrUnavailable)
}

func TestSuggestionSourcePreservesActualContentAndProjectScope(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "NEW", "status": "RECEIVED", "raw_content": "Exact source, including unknowns"}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		row, err := m.suggestionSourceOn(ctx, tx, 7, 20, 10)
		require.NoError(t, err)
		require.Equal(t, "Exact source, including unknowns", *row.Content)
		require.Equal(t, 1, row.Version)
		_, err = m.suggestionSourceOn(ctx, tx, 7, 21, 10)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		_, err = m.suggestionSourceOn(context.Background(), tx, 7, 20, 10)
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
	_, err := m.suggestionSourceOn(ctx, db, 7, 20, 10)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("status", "CONVERTED").Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.suggestionSourceOn(ctx, tx, 7, 20, 10); return err }), gorm.ErrRecordNotFound)
}

func TestSuggestionSourcesRequiresTransactionTenantIdentityAndBoundedCursor(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	_, err := m.suggestionSourcesOn(ctx, db, 7, 20, 0, 1)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, limit := range []int{0, 101} {
			_, err := m.suggestionSourcesOn(ctx, tx, 7, 20, 0, limit)
			require.ErrorIs(t, err, ErrInvalidArgument)
		}
		_, err := m.suggestionSourcesOn(ctx, tx, 7, 20, -1, 1)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = m.suggestionSourcesOn(ctx, tx, 8, 20, 0, 1)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = m.suggestionSourcesOn(context.Background(), tx, 7, 20, 0, 1)
		require.ErrorIs(t, err, ErrInvalidArgument)
		page, err := m.suggestionSourcesOn(ctx, tx, 7, 20, 0, 1)
		require.NoError(t, err)
		require.Empty(t, page.Items, "converted sources never re-enter Agent triage discovery")
		require.False(t, page.HasMore)
		return nil
	}))
}
