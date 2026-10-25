package templates

import (
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestRuntimeCostConnectorRejectsMissingOrSecondBinding(t *testing.T) {
	m, _, _ := templateHarness(t)
	s := module.NewServices()
	require.NoError(t, m.ProvideServices(s))
	require.Error(t, m.ProvideServices(s))
	require.NoError(t, s.Seal())
	connector, e := module.Resolve[RuntimeCostsConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, connector.Bind(RuntimeCosts{}), ErrInvalid)
	costs := RuntimeCosts{WorkOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) { return 5, true, nil }}
	require.NoError(t, connector.Bind(costs))
	require.ErrorIs(t, connector.Bind(costs), ErrInvalid)
}
func TestLoopEscalationRequiresRecorderAndAtomicOutbox(t *testing.T) {
	m, db, ctx := templateHarness(t)
	require.ErrorIs(t, m.escalateLoopOn(ctx, db, 1, 99, 88, "hash", "key", "BUDGET", 11), ErrUnavailable)
	calls := 0
	m.escalate = func(context.Context, *gorm.DB, governance.EscalationFact) (guid.ID, error) { calls++; return 1, nil }
	failure := errors.New("outbox unavailable")
	m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
	require.ErrorIs(t, m.escalateLoopOn(ctx, db, 1, 99, 88, "hash", "key", "BUDGET", 11), failure)
	require.Zero(t, calls)
}
