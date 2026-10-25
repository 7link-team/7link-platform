package agents

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestRetirementHandoverConnectorRequiresCompleteSingleBinding(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideRetirementHandover(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[RetirementHandoverConnector](s)
	require.NoError(t, err)
	require.NotNil(t, c.AuthorizeOn)
	require.ErrorIs(t, c.Bind(RetirementHandover{}), ErrConflict)
	v := RetirementHandover{LaunchOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (guid.ID, error) { return 1, nil }, ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
		return HandoverArtifact{}, nil
	}, SystemOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (HandoverArtifact, error) {
		return HandoverArtifact{}, nil
	}}
	require.NoError(t, c.Bind(v))
	require.ErrorIs(t, c.Bind(v), ErrConflict)
	require.Error(t, c.AuthorizeOn(t.Context(), nil, 1, 7, 8))
}
