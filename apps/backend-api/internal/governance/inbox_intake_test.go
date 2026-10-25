package governance

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIntakeInboxOrdersAuthorizedSourceBeforeLimitAndCursor(t *testing.T) {
	m, _, ctx, _ := settingsHarness(t)
	s := module.NewServices()
	require.NoError(t, module.Provide[intake.PendingTriage](s, func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]intake.PendingTriageFact, error) {
		return []intake.PendingTriageFact{{ID: 8, ProjectID: 3, Title: "Later", CreatedAt: time.Unix(2, 0)}, {ID: 7, ProjectID: 3, Title: "Earlier", CreatedAt: time.Unix(1, 0)}}, nil
	}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindIntakeInbox(s))
	// Aggregate reader also validates current principal and tenant visibility.
	req := InboxRequest{TenantID: 1, PrincipalID: 2, ProjectID: 3, Kind: "INTAKE_TRIAGE", Limit: 1}
	reader, ok := m.inbox.sources["INTAKE_TRIAGE"]
	require.True(t, ok)
	rows, err := reader.Read(ctx, req)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, guid.ID(7), rows[0].ID)
	req.After = &rows[0].InboxKey
	rows, err = reader.Read(ctx, req)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, guid.ID(8), rows[0].ID)
	_, err = reader.Read(context.Background(), req)
	require.ErrorIs(t, err, ErrForbidden)
}
