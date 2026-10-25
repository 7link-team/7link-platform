package governance

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMemoryRuleInboxPagesOnlySourceAuthorizedRules(t *testing.T) {
	m, _, ctx, _ := settingsHarness(t)
	available := true
	created := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	m.pendingRules = func(_ context.Context, tx *gorm.DB, tenant, project guid.ID) ([]knowledge.PendingRule, error) {
		require.NotNil(t, tx)
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		if !available {
			return nil, knowledge.ErrUnavailable
		}
		return []knowledge.PendingRule{{ID: 8, ProjectID: 3, Title: "Later", CreatedAt: created.Add(time.Hour)}, {ID: 7, ProjectID: 3, Title: "Pending", CreatedAt: created}}, nil
	}
	req := InboxRequest{TenantID: 1, PrincipalID: 2, ProjectID: 3, Kind: "MEMORY_RULE_APPROVAL", Limit: 1}
	rows, err := m.memoryRuleInbox(ctx, req)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "memory", rows[0].ObjectType)
	require.Equal(t, guid.ID(7), rows[0].ID)
	req.After = &rows[0].InboxKey
	rows, err = m.memoryRuleInbox(ctx, req)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, guid.ID(8), rows[0].ID)
	other := req
	other.PrincipalID = 3
	_, err = m.memoryRuleInbox(ctx, other)
	require.ErrorIs(t, err, ErrForbidden)
	available = false
	_, err = m.memoryRuleInbox(ctx, req)
	require.ErrorIs(t, err, ErrUnavailable)
}
