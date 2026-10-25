package execution

import (
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/realtime"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRealtimeTopicsAdapterDelegatesOnlyExactOwnerTopicShapes(t *testing.T) {
	a := &realtimeTopicsAdapter{enabled: true}
	require.True(t, a.Enabled())
	require.Equal(t, "realtime-topics", a.ModuleName())
	require.Equal(t, []string{delivery.Name, fleet.Name, collab.Name, Name, realtime.Name}, a.ModuleDependencies())
	registered := map[string]realtime.TopicAuthorizer{}
	calls := 0
	owner := func(_ context.Context, tenant, principal, id guid.ID, collection bool) (bool, error) {
		calls++
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 10, principal)
		if !collection {
			require.EqualValues(t, 2, id)
		}
		return true, nil
	}
	s := module.NewServices()
	require.NoError(t, module.Provide[realtime.TopicAuthorizers](s, realtime.TopicAuthorizers{Register: func(kind string, fn realtime.TopicAuthorizer) error { registered[kind] = fn; return nil }}))
	require.NoError(t, module.Provide[delivery.RealtimeTopics](s, delivery.RealtimeTopics{Authorize: owner, AuthorizeWork: owner}))
	require.NoError(t, module.Provide[fleet.RealtimeTopics](s, fleet.RealtimeTopics{Authorize: owner}))
	require.NoError(t, module.Provide[collab.RealtimeTopics](s, collab.RealtimeTopics{AuthorizeChannel: owner, AuthorizeThread: owner}))
	require.NoError(t, module.Provide[RealtimeTopics](s, RealtimeTopics{Authorize: owner}))
	require.NoError(t, s.Seal())
	require.NoError(t, a.InjectServices(s))
	require.Len(t, registered, 8)
	for _, kind := range []string{"project", "projects", "work_item", "machine", "machines", "channel", "thread", "execution"} {
		collection := kind == "projects" || kind == "machines"
		allowed, err := registered[kind](t.Context(), 7, 10, realtime.Topic{ID: 2, Kind: kind, Collection: collection})
		require.NoError(t, err)
		require.True(t, allowed)
		allowed, err = registered[kind](t.Context(), 7, 10, realtime.Topic{ID: 2, Kind: kind, Collection: !collection})
		require.NoError(t, err)
		require.False(t, allowed)
	}
	require.Equal(t, 8, calls)
	missing := module.NewServices()
	require.NoError(t, missing.Seal())
	require.Error(t, a.InjectServices(missing))
}
