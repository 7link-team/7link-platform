package realtime

import (
	"context"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"github.com/stretchr/testify/require"
)

func TestTopicGrammarAndAuthorizationFailClosed(t *testing.T) {
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
	require.NoError(t, m.register("project", func(context.Context, guid.ID, guid.ID, Topic) (bool, error) { return true, nil }))
	require.Error(t, m.register("project", func(context.Context, guid.ID, guid.ID, Topic) (bool, error) { return true, nil }))
	require.Error(t, m.register("inbox", nil))
	require.Error(t, m.register("Bad", nil))
	for _, raw := range []string{"", "tenant/1/work-items/2", "tenant/01/project/2", "tenant/1/project/*", "tenant/1/project/02", "tenant/1/project/0", "tenant/1/project/2/a-b", "tenant/1/project/2/channel/extra"} {
		_, _, err := parseTopic(raw)
		require.ErrorIs(t, err, ErrInvalid, raw)
	}
	for _, test := range []struct {
		raw, reason string
		allowed     bool
	}{{"tenant/1/project/2", "", true}, {"tenant/2/project/2", "tenant_mismatch", false}, {"tenant/1/project/2/channel", "server_only", false}, {"tenant/1/inbox/10", "", true}, {"tenant/1/inbox/11", "forbidden", false}, {"tenant/1/inbox", "forbidden", false}, {"tenant/1/unknown/2", "forbidden", false}} {
		allowed, reason, err := m.authorize(t.Context(), 1, 10, test.raw)
		require.NoError(t, err)
		require.Equal(t, test.allowed, allowed)
		require.Equal(t, test.reason, reason)
	}
	m.sealed = true
	require.Error(t, m.register("work_item", func(context.Context, guid.ID, guid.ID, Topic) (bool, error) { return true, nil }))
}
