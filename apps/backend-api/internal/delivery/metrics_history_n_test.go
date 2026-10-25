package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestQualityHistoryRejectsInvalidWindowsAndUnavailableSources(t *testing.T) {
	h := newResultFixture(t)
	read := func(from, to string) (DeliveryQualityHistory, error) {
		var out DeliveryQualityHistory
		err := h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = h.m.deliveryQualityOn(h.actor(12), tx, 1, from, to)
			return err
		})
		return out, err
	}
	for _, dates := range [][2]string{{"invalid", "2026-10-21"}, {"2026-10-19", "invalid"}, {"2026-10-21", "2026-10-19"}, {"2026-01-01", "2026-10-21"}} {
		out, err := read(dates[0], dates[1])
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.Empty(t, out.Days)
	}
	for _, table := range []string{h.m.service.names.Projects.String(), h.m.service.names.WorkItems.String(), h.m.service.names.ResultActions.String(), h.m.service.names.VerificationRuns.String()} {
		t.Run(table, func(t *testing.T) {
			failure := errors.New("historical receipt source unavailable")
			fired := false
			require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("regression_history", func(tx *gorm.DB) {
				if strings.Contains(tx.Statement.Table, table) || (tx.Statement.TableExpr != nil && strings.Contains(tx.Statement.TableExpr.SQL, table)) {
					fired = true
					_ = tx.AddError(failure)
				}
			}))
			_, err := read("2026-10-19", "2026-10-21")
			require.NoError(t, h.db.Callback().Query().Remove("regression_history"))
			require.True(t, fired, "fault must reach the actual source query")
			require.ErrorIs(t, err, failure, "outage cannot become a zero quality score")
		})
	}
	policy := h.m.workPolicy
	defer func() { h.m.workPolicy = policy }()
	for _, malformed := range []bool{false, true} {
		calls := 0
		failure := errors.New("quality policy unavailable")
		h.m.workPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
			calls++
			// Enumeration and current project authorization still succeed; fail
			// the final row policy to prove historical reads remain fenced.
			if calls <= 2 {
				return policy(ctx, tx, tenant, project, permission, actor)
			}
			if malformed {
				return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
			}
			return authz.DataConstraint{}, failure
		}
		_, err := read("2026-10-19", "2026-10-21")
		require.Error(t, err)
		if !malformed {
			require.ErrorIs(t, err, failure)
		}
	}
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
	}
	out, err := read("2026-10-19", "2026-10-21")
	require.NoError(t, err)
	require.Len(t, out.Days, 2)
	for _, day := range out.Days {
		require.Zero(t, day.Received)
		require.Zero(t, day.VerificationPassed)
		require.Nil(t, day.AcceptanceRate, "invisible source receipts cannot imply an observed rate")
	}
}
