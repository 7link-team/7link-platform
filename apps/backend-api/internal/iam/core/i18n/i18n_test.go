package corei18n

import (
	"testing"

	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterPublishesEveryIAMLocale(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, Register())

	for _, locale := range platformi18n.Supported() {
		ctx := platformi18n.WithLocale(t.Context(), locale.Code)
		assert.NotEqual(t, RoleNameConflict, platformi18n.TContext(ctx, RoleNameConflict), locale.Code)
		assert.NotEqual(t, Unavailable, platformi18n.TContext(ctx, Unavailable), locale.Code)
	}
	assert.Error(t, Register(), "a module locale namespace is registered exactly once")
}
