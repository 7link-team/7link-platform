package notification

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/governance"
	governancei18n "7link.com/platform/internal/governance/i18n"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnBudgetPersistsLocalizedInboxAndRevocation(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, governancei18n.Register())
	spent := int64(800000)
	f.m.spawnFacts = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID, kind string) (governance.SpawnNotificationFact, bool, error) {
		return governance.SpawnNotificationFact{GrantID: id, GrantorID: 11, State: "ACTIVE", Cap: 1000000, Spent: spent}, tenant == 7, nil
	}
	f.m.candidates = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]guid.ID, error) {
		if after == 0 {
			return []guid.ID{11}, nil
		}
		return nil, nil
	}
	e := events.Envelope{EventID: 1, TenantID: 7, EventType: "spawn_grant.budget_threshold", Subject: events.Subject{Kind: "spawn_grant", ID: 55}, Data: json.RawMessage(`{"granted_by":"11","spent_micros":"800000","scope":{"budget_cap_micros":1000000}}`)}
	consume := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error { return f.m.consumeSpawnBudget(t.Context(), tx, e) })
	}
	require.NoError(t, consume())
	require.NoError(t, consume())
	var rows []model.Notification
	require.NoError(t, f.db.Table(f.m.names.Notifications.String()).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "spawn_grant_manage", rows[0].Permission)
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		v, err := view(i18n.WithLocale(context.Background(), locale), &rows[0])
		require.NoError(t, err)
		require.NotEmpty(t, v.Title)
		require.Contains(t, v.Body, "800000")
		require.Equal(t, "/admin/7/governance/spawn-grants?spawn_grant_id=55", v.LinkPath)
	}
	f.allow = false
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		visible, err := f.m.visibleOn(t.Context(), tx, &rows[0], guid.ID(11))
		require.NoError(t, err)
		require.False(t, visible)
		return nil
	}))
	f.allow = true
	e.Subject.ID = 56
	abort := errors.New("rollback")
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, f.m.consumeSpawnBudget(t.Context(), tx, e))
		return abort
	}), abort)
	var n int64
	require.NoError(t, f.db.Table(f.m.names.Notifications.String()).Count(&n).Error)
	require.EqualValues(t, 1, n)
	spent = 799999
	require.ErrorIs(t, consume(), ErrInvalid)
}
