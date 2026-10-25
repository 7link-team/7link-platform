package notification

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestToolAuthFailuresCannotCreateUnverifiedNotice(t *testing.T) {
	for _, mode := range []string{"identity", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			source := agents.ToolAuthFailure{TenantID: 7, ToolID: 91, Title: "Remote tool"}
			switch mode {
			case "identity":
				source.ToolID = 0
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.toolAuthFailureOn(t.Context(), tx, source) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestToolAuthNoticeTargetsCurrentCatalogManagers(t *testing.T) {
	f := newFixture(t)
	f.m.tenantPermission = func(_ context.Context, _ *gorm.DB, tenant guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, "tool_catalog_manage", permission)
		return authz.DataConstraint{AllowAll: actor == 11}, nil
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.toolAuthFailureOn(t.Context(), tx, agents.ToolAuthFailure{TenantID: 1, ToolID: 77, Title: "Remote MCP"})
	}))
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq("agents.tool_auth_failed")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, guid.ID(11), rows[0].RecipientPrincipalID)
	require.Equal(t, "tool_catalog_manage", rows[0].Permission)
}
