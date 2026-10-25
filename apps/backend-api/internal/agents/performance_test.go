package agents

import (
	"context"
	"math"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func installPerformanceFixture(m *Module) {
	m.performance.read = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, _ int, kind string, _ time.Time) ([]AgentPerformanceMeasures, error) {
		cost := int64(303)
		rows := []AgentPerformanceMeasures{{WorkItemType: "BUG", FirstDecisions: 4, FirstPassAccepts: 3, Claims: 2, ReworkDecisions: 3, AcceptedItems: 1, CostOnAcceptedMicros: &cost}, {WorkItemType: "TASK", FirstDecisions: 1, FirstPassAccepts: 1}}
		if kind == "BUG" {
			return rows[:1], nil
		}
		return rows, nil
	}
	m.performance.running = func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error) { return 2, nil }
	m.performance.work.CountOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error) { return 1, nil }
	m.profiles.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentProfile, error) {
		return fleet.AgentProfile{MaxParallelItems: 4}, nil
	}
}

func TestAgentPerformanceHTTPWindowsScopeSamplesAndCurrentAuthority(t *testing.T) {
	m, _, h, api := agentHTTPHarness(t)
	installPerformanceFixture(m)
	path := "/tenants/1/agents/7/stats"
	r := callAgent(t, h, agentContext(t, 2), "GET", path, "", "")
	require.Equal(t, 200, r.Code, r.Body.String())
	for _, text := range []string{`"fpa":0.8`, `"fpa":null`, `"rework_rate":1.5`, `"cpa_micros":303`, `"active_items":1`, `"max_parallel_items":4`, `"running_executions":2`, `"window_days":30`, `CURRENT_READER_VISIBLE_WORK_AND_EXECUTIONS`} {
		require.Contains(t, r.Body.String(), text)
	}
	r = callAgent(t, h, agentContext(t, 2), "GET", path+"?window_days=90&work_item_type=BUG", "", "")
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), `"window_days":90`)
	require.NotContains(t, r.Body.String(), `"work_item_type":"TASK"`)
	for _, query := range []string{"?window_days=7", "?work_item_type=UNSUPPORTED"} {
		require.Equal(t, 422, callAgent(t, h, agentContext(t, 2), "GET", path+query, "", "").Code)
	}
	for _, actor := range []guid.ID{7, 8, 9} {
		require.Equal(t, 403, callAgent(t, h, agentContext(t, actor), "GET", path, "", "").Code)
	}
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Equal(t, 403, callAgent(t, h, agentContext(t, 2), "GET", path, "", "").Code)
	require.Equal(t, "agent_stats.read", api.OpenAPI().Paths["/tenants/{tenant_id}/agents/{agent_principal_id}/stats"].Get.OperationID)
}

func TestAgentPerformanceMissingFactsUnknownCostsOverflowAndZeroDenominators(t *testing.T) {
	m, db, _ := agentHarness(t)
	installPerformanceFixture(m)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		ctx := agentContext(t, 2)
		m.performance.read = func(context.Context, *gorm.DB, guid.ID, guid.ID, int, string, time.Time) ([]AgentPerformanceMeasures, error) {
			return []AgentPerformanceMeasures{{WorkItemType: "BUG", AcceptedItems: 1}}, nil
		}
		v, err := m.performanceOn(ctx, tx, 1, 7, 30, "")
		require.NoError(t, err)
		require.Nil(t, v.CPAMicros)
		require.Nil(t, v.FPA)
		require.Nil(t, v.ReworkRate)
		m.performance.read = func(context.Context, *gorm.DB, guid.ID, guid.ID, int, string, time.Time) ([]AgentPerformanceMeasures, error) {
			return []AgentPerformanceMeasures{}, nil
		}
		v, err = m.performanceOn(ctx, tx, 1, 7, 90, "")
		require.NoError(t, err)
		require.Nil(t, v.CPAMicros)
		require.Empty(t, v.ByType)
		cost := int64(math.MaxInt64)
		m.performance.read = func(context.Context, *gorm.DB, guid.ID, guid.ID, int, string, time.Time) ([]AgentPerformanceMeasures, error) {
			return []AgentPerformanceMeasures{{AcceptedItems: 1, CostOnAcceptedMicros: &cost}, {AcceptedItems: 1, CostOnAcceptedMicros: &cost}}, nil
		}
		_, err = m.performanceOn(ctx, tx, 1, 7, 30, "")
		require.ErrorIs(t, err, ErrUnavailable)
		m.performance.running = nil
		_, err = m.performanceOn(ctx, tx, 1, 7, 30, "")
		require.ErrorIs(t, err, ErrUnavailable)
		return nil
	}))
	services := module.NewServices()
	require.NoError(t, m.providePerformance(services))
	require.NoError(t, services.Seal())
	c, err := module.Resolve[AgentPerformanceConnector](services)
	require.NoError(t, err)
	require.ErrorIs(t, c.BindHistory(nil), ErrConflict)
	history := func(context.Context, *gorm.DB, guid.ID, guid.ID, int, string, time.Time) ([]AgentPerformanceMeasures, error) {
		return nil, nil
	}
	m.performance.read = nil
	require.NoError(t, c.BindHistory(history))
	require.ErrorIs(t, c.BindHistory(history), ErrConflict)
	require.ErrorIs(t, c.BindRunning(nil), ErrConflict)
	fn := func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error) { return 0, nil }
	require.NoError(t, c.BindRunning(fn))
	require.ErrorIs(t, c.BindRunning(fn), ErrConflict)
}

func TestPerformanceSourceFailureNeverReportsFabricatedEmptySamples(t *testing.T) {
	for _, mode := range []string{"history", "work", "running", "profile"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			installPerformanceFixture(m)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.performanceOn(agentContext(t, 2), tx, 1, 7, 7, ""); return e }), ErrInvalid)
			switch mode {
			case "history":
				m.performance.read = func(context.Context, *gorm.DB, guid.ID, guid.ID, int, string, time.Time) ([]AgentPerformanceMeasures, error) {
					return nil, ErrUnavailable
				}
			case "work":
				m.performance.work.CountOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error) { return 0, ErrUnavailable }
			case "running":
				m.performance.running = func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error) { return 0, ErrUnavailable }
			case "profile":
				m.profiles.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentProfile, error) {
					return fleet.AgentProfile{}, fleet.ErrUnavailable
				}
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.performanceOn(agentContext(t, 2), tx, 1, 7, 30, ""); return e }), ErrUnavailable)
		})
	}
}
