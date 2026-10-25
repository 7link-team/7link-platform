package intake

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestDispositionRoundBindingCannotReplaceExistingOwner(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideDispositionRound(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[DispositionRoundConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, c.Bind(nil), ErrInvalidArgument)
	closer := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, string, string) error { return nil }
	require.NoError(t, c.Bind(closer))
	require.ErrorIs(t, c.Bind(closer), ErrInvalidArgument)
	require.ErrorIs(t, c.BindNotification(nil), ErrInvalidArgument)
	send := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, *int64) (DispositionDelivery, error) {
		return DispositionDelivery{State: "UNAVAILABLE"}, nil
	}
	require.NoError(t, c.BindNotification(send))
	require.ErrorIs(t, c.BindNotification(send), ErrInvalidArgument)
}
