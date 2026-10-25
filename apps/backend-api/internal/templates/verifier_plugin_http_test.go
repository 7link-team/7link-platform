package templates

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestVerifierPluginHTTPInventoryRequiresCurrentAuthority(t *testing.T) {
	m, _, ctx := templateHarness(t)
	page, e := m.verifierPluginsHTTP(ctx, &struct{}{})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 2)
	require.False(t, page.Body.Data.HasMore)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.verifierPluginsHTTP(ctx, &struct{}{})
	assertStatus(t, e, 403)
}
