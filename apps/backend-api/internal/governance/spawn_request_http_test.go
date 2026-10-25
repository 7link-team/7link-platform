package governance

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAgentSpawnRequestHTTPReplayRechecksCurrentGrant(t *testing.T) {
	m, db, human, agent, _, in := agentSpawnFixture(t)
	store := idempotency.NewModule(m.runtime)
	s := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](s, m.seed.ids))
	require.NoError(t, s.Seal())
	require.NoError(t, store.InjectServices(s))
	source := store.MigrationSource()
	source.Prefix = "g_"
	raw, e := db.DB()
	require.NoError(t, e)
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	m.delegations.replay = store
	input := &agentSpawnRequestCreateInput{TenantID: "1", Key: "spawn-request", Body: in}
	first, e := m.createAgentSpawnRequestHTTP(agent, input)
	require.NoError(t, e)
	again, e := m.createAgentSpawnRequestHTTP(agent, input)
	require.NoError(t, e)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, first.Body.Data.ID, again.Body.Data.ID)
	m.delegations.members = delegationMembers{false}
	_, e = m.createAgentSpawnRequestHTTP(agent, input)
	require.Error(t, e)
	require.EqualValues(t, 1, seedCount(t, db, m.seed.names.SpawnRequests.String()))
	m.delegations.members = delegationMembers{true}
	listed, e := m.listAgentSpawnRequestsHTTP(human, &agentSpawnRequestListInput{TenantID: "1", Limit: 10})
	require.NoError(t, e)
	require.Len(t, listed.Body.Data, 1)
	require.Equal(t, first.Body.Data.ID, listed.Body.Data[0].ID)
	change := &agentSpawnRequestChangeInput{TenantID: "1", RequestID: first.Body.Data.ID.String(), Key: "reject-request"}
	change.Body.Version = 1
	change.Body.Action = "REJECT"
	change.Body.Reason = "Human declines additional capacity"
	changed, e := m.changeAgentSpawnRequestHTTP(human, change)
	require.NoError(t, e)
	require.Equal(t, "REJECTED", changed.Body.Data.State)
	replayed, e := m.changeAgentSpawnRequestHTTP(human, change)
	require.NoError(t, e)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, changed.Body.Data.Version, replayed.Body.Data.Version)
	require.EqualValues(t, 3, seedCount(t, db, "event_probe"))
	listed, e = m.listAgentSpawnRequestsHTTP(human, &agentSpawnRequestListInput{TenantID: "1", Limit: 1, Offset: 1})
	require.NoError(t, e)
	require.Empty(t, listed.Body.Data)
	m.delegations.members = delegationMembers{false}
	_, e = m.changeAgentSpawnRequestHTTP(human, change)
	require.Error(t, e)
	_, e = m.listAgentSpawnRequestsHTTP(human, &agentSpawnRequestListInput{TenantID: "1", Limit: 10})
	require.Error(t, e)
}

func TestAgentSpawnRequestHTTPRejectsInvalidIdentitiesAndUnavailableStorage(t *testing.T) {
	m := &Module{}
	for _, in := range []*agentSpawnRequestChangeInput{{TenantID: "bad", RequestID: "1"}, {TenantID: "1", RequestID: "bad"}} {
		out, err := m.changeAgentSpawnRequestHTTP(t.Context(), in)
		require.Error(t, err)
		require.Nil(t, out)
	}
	for _, tenant := range []string{"bad", "1"} {
		out, err := m.listAgentSpawnRequestsHTTP(t.Context(), &agentSpawnRequestListInput{TenantID: tenant, Limit: 10})
		require.Error(t, err)
		require.Nil(t, out)
	}
}
