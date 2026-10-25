package delivery

import (
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestIterationWorkProviderRegistersSourceOwnedReader(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideIterationWork(s))
	require.NoError(t, s.Seal())
	reader, err := module.Resolve[PlanningWorkIdentityOn](s)
	require.NoError(t, err)
	require.NotNil(t, reader)
	// Invalid actor/scope is denied before any persistence can be accessed.
	_, err = reader(context.Background(), nil, 1, 2, 3)
	require.Error(t, err)
}
