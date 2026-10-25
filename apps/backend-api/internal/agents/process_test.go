package agents

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestProcessHistoryPersistsRealObservationsWithBoundedTenantPaging(t *testing.T) {
	m, db, _ := agentHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < 4; i++ {
			require.NoError(t, m.observeProcessOn(t.Context(), tx, fleet.AgentProcessTransition{TenantID: 1, AgentPrincipalID: 7, Kind: "CRASHED", RestartCount: i + 1, OccurredAt: time.Now().UTC().Add(time.Duration(i) * time.Second)}))
		}
		require.NoError(t, m.observeProcessOn(t.Context(), tx, fleet.AgentProcessTransition{TenantID: 2, AgentPrincipalID: 7, Kind: "STARTED", OccurredAt: time.Now().UTC()}))
		page, e := m.processHistoryOn(agentContext(t, 2), tx, 1, 7, ProcessHistoryInput{Limit: 2, Search: "crash"})
		require.NoError(t, e)
		require.Len(t, page.Items, 2)
		require.True(t, page.HasMore)
		require.Equal(t, 4, page.Items[0].RestartCount)
		last, e := m.processHistoryOn(agentContext(t, 2), tx, 1, 7, ProcessHistoryInput{Limit: 2, Offset: 2})
		require.NoError(t, e)
		require.Len(t, last.Items, 2)
		require.False(t, last.HasMore)
		require.Equal(t, 2, last.Items[0].RestartCount)
		from, to := page.Items[1].OccurredAt, page.Items[0].OccurredAt
		filtered, e := m.processHistoryOn(agentContext(t, 2), tx, 1, 7, ProcessHistoryInput{Limit: 50, From: from, To: to})
		require.NoError(t, e)
		require.Len(t, filtered.Items, 2)
		_, e = m.processHistoryOn(agentContext(t, 2), tx, 1, 7, ProcessHistoryInput{Limit: 50, From: to, To: from})
		require.ErrorIs(t, e, ErrInvalid)
		_, e = m.processHistoryOn(agentContext(t, 2), tx, 1, 7, ProcessHistoryInput{Limit: 50, Kind: "UNKNOWN"})
		require.ErrorIs(t, e, ErrInvalid)
		_, e = m.processHistoryOn(agentContext(t, 7), tx, 1, 7, ProcessHistoryInput{Limit: 50})
		require.ErrorIs(t, e, ErrForbidden)
		_, e = m.processHistoryOn(agentContext(t, 2), tx, 1, 7, ProcessHistoryInput{Limit: 201})
		require.ErrorIs(t, e, ErrInvalid)
		return nil
	}))
}

func TestRunStateRechecksHumanAuthorityAndFailsWithoutActualRunnerAdapter(t *testing.T) {
	m, db, _ := agentHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.runStateOn(agentContext(t, 7), tx, 1, 7, "ACTIVE")
		require.ErrorIs(t, e, ErrForbidden)
		_, e = m.runStateOn(agentContext(t, 2), tx, 1, 7, "ACTIVE")
		require.ErrorIs(t, e, ErrUnavailable)
		m.profiles.RunStateOn = func(_ context.Context, _ *gorm.DB, tenant, agent guid.ID, desired string) (fleet.AgentRunState, error) {
			require.Equal(t, guid.ID(1), tenant)
			require.Equal(t, guid.ID(7), agent)
			return fleet.AgentRunState{State: desired, ProcessStatus: "STOPPING"}, nil
		}
		value, e := m.runStateOn(agentContext(t, 2), tx, 1, 7, "STOPPED")
		require.NoError(t, e)
		require.Equal(t, "STOPPED", value.State)
		return nil
	}))
}

func TestProcessHistoryHTTPRegistrationAndOptionalDateBounds(t *testing.T) {
	_, _, handler, api := agentHTTPHarness(t)
	require.Contains(t, api.OpenAPI().Paths, "/tenants/{tenant_id}/agents/{agent_principal_id}/process-events")
	for _, suffix := range []string{"", "?from=2026-01-01T00:00:00Z&to=2026-12-31T00:00:00Z"} {
		response := callAgent(t, handler, agentContext(t, 2), "GET", "/tenants/1/agents/7/process-events"+suffix, "", "")
		require.Equal(t, 200, response.Code, response.Body.String())
	}
	for _, suffix := range []string{"?from=invalid", "?from=2026-12-31T00:00:00Z&to=2026-01-01T00:00:00Z", "?kind=UNKNOWN"} {
		response := callAgent(t, handler, agentContext(t, 2), "GET", "/tenants/1/agents/7/process-events"+suffix, "", "")
		require.Equal(t, 422, response.Code, response.Body.String())
	}
}

func TestRunStatePublicCommandPersistsActualRunnerFactsAndReauthorizesReplay(t *testing.T) {
	m, db, h, _ := agentHTTPHarness(t)
	calls := 0
	m.profiles.RunStateOn = func(_ context.Context, tx *gorm.DB, tenant, agent guid.ID, desired string) (fleet.AgentRunState, error) {
		calls++
		return fleet.AgentRunState{State: desired, ProcessStatus: "STOPPING"}, tx.Exec("INSERT INTO audit_probe VALUES ('runner-stop')").Error
	}
	path := "/tenants/1/agents/7/run-state"
	ctx := agentContext(t, 2)
	first := callAgent(t, h, ctx, "PUT", path, "actual-stop", `{"desired":"STOPPED"}`)
	require.Equal(t, 202, first.Code, first.Body.String())
	require.Contains(t, first.Body.String(), "STOPPING")
	replay := callAgent(t, h, ctx, "PUT", path, "actual-stop", `{"desired":"STOPPED"}`)
	require.Equal(t, 202, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	require.Equal(t, 1, calls)
	m.audit = nil
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.runStateOn(ctx, tx, 1, 7, "STOPPED"); return e }))
	var effects int64
	require.NoError(t, db.Table("audit_probe").Where("event_type='runner-stop'").Count(&effects).Error)
	require.EqualValues(t, 1, effects)
	empty, err := m.processHistoryHTTP(ctx, &ProcessHistoryInput{AgentPath: AgentPath{TenantID: "bad", AgentPrincipalID: "7"}, Limit: 50})
	require.Error(t, err)
	require.Nil(t, empty)
}
