package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestScopeChangeHistoryUsesCurrentRequirementScope(t *testing.T) {
	m, _, ctx, row, body := scopeHarness(t)
	created, err := m.scopeChangeHTTP(ctx, &scopeChangeInput{EntityID: "99", RequirementID: row.ID.String(), IfMatch: fmtETag(row.Version), Body: body})
	require.NoError(t, err)
	page, err := m.changeHistoryHTTP(ctx, &changeHistoryInput{EntityID: "99", RequirementID: row.ID.String(), Limit: 200})
	require.NoError(t, err)
	require.Len(t, page.Body.Data, 1)
	require.Equal(t, created.Body.Data.ID, page.Body.Data[0].ID)
	impacts, err := m.changeImpactsHTTP(ctx, &changeImpactsInput{EntityID: "99", RequirementID: row.ID.String(), ChangeRecordID: created.Body.Data.ID.String(), Limit: 200})
	require.NoError(t, err)
	require.Empty(t, impacts.Body.Data)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}, nil
	}
	_, err = m.changeHistoryHTTP(ctx, &changeHistoryInput{EntityID: "99", RequirementID: row.ID.String(), Limit: 200})
	assertWorkStatus(t, err, 404)
}

func TestScopeImpactsFilterCurrentWorkScopeAndPageAfterFiltering(t *testing.T) {
	m, db, ctx, id, _ := staleBaselineFixture(t)
	w := query.BindWorkItems(db, m.service.names)
	work, err := w.WithContext(ctx).Where(w.ID.Eq(id)).First()
	require.NoError(t, err)
	c := query.BindChanges(db, m.service.names)
	change, err := c.WithContext(ctx).Where(c.RequirementID.Eq(*work.RequirementID)).First()
	require.NoError(t, err)
	items, count, err := m.changeImpactsOn(ctx, db, 1, 99, *work.RequirementID, change.ID, 11, 0, 200)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.Len(t, items, 1)
	require.Equal(t, id, items[0].WorkItemID)
	items, count, err = m.changeImpactsOn(ctx, db, 1, 99, *work.RequirementID, change.ID, 11, 100, 200)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.Empty(t, items)
	m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		if permission == "work_item_read" {
			return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
		}
		return authz.DataConstraint{AllowAll: true}, nil
	}
	items, count, err = m.changeImpactsOn(ctx, db, 1, 99, *work.RequirementID, change.ID, 11, 0, 200)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Empty(t, items)
	_, _, err = m.changeImpactsOn(ctx, db, 1, 99, *work.RequirementID, 333, 11, 0, 200)
	require.ErrorIs(t, err, ErrProjectNotFound)
	_, _, err = m.changeImpactsOn(ctx, db, 1, 99, *work.RequirementID, change.ID, 11, -1, 200)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = m.changeHistoryOn(ctx, db, 1, 99, *work.RequirementID, 11, 0, 201)
	require.ErrorIs(t, err, ErrInvalidArgument)
}
