package agents

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestLiveRuntimeFactsPreserveObservedActivityAndFailClosed(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	for _, tc := range []struct {
		found bool
		err   error
	}{{true, nil}, {false, nil}, {false, errors.New("fleet unavailable")}} {
		m.runtimeFacts = fleet.AgentRuntimeFacts{ByPrincipalOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
			return fleet.AgentRuntimeFact{MachineState: "ONLINE", ProcessStatus: "STARTED"}, tc.found, tc.err
		}}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			v, e := m.liveOn(ctx, tx, 1, 7)
			if !tc.found || tc.err != nil {
				require.Error(t, e)
			} else {
				require.NoError(t, e)
				require.Equal(t, "OFFLINE", v.Activity)
				require.Equal(t, "ONLINE", v.Runtime.MachineState)
			}
			return nil
		}))
	}
}
