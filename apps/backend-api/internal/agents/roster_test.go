package agents

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestRosterConnectorRejectsMissingAndDuplicateSources(t *testing.T) {
	m, _, _ := agentHarness(t)
	services := module.NewServices()
	require.NoError(t, m.provideRoster(services))
	require.NoError(t, services.Seal())
	connector, e := module.Resolve[AgentRosterAnalyticsConnector](services)
	require.NoError(t, e)
	require.ErrorIs(t, connector.Bind(nil), ErrInvalid)
	source := func(context.Context, *gorm.DB, guid.ID, []guid.ID) (map[guid.ID]RosterAnalytics, error) {
		return map[guid.ID]RosterAnalytics{7: {FirstDecisions: 5, FirstPassAccepts: 3}}, nil
	}
	require.NoError(t, connector.Bind(source))
	require.ErrorIs(t, connector.Bind(source), ErrInvalid)
	got, e := m.rosterAnalytics(t.Context(), nil, 1, []guid.ID{7})
	require.NoError(t, e)
	require.EqualValues(t, 3, got[7].FirstPassAccepts)
}
