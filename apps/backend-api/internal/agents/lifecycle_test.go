package agents

import (
	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestResetHistoryScopesOrdersPagesAndRechecksAuthority(t *testing.T) {
	m, db, _ := agentHarness(t)
	for _, row := range []model.Reset{
		{ID: 10, TenantID: 1, AgentPrincipalID: 7, Kind: "RESTART", State: "DONE", Reason: "Earlier completed reset", RequestedBy: 2, CreatedAt: m.now()},
		{ID: 11, TenantID: 1, AgentPrincipalID: 7, Kind: "SESSION", State: "FAILED", Reason: "Runner rejected reset", RequestedBy: 2, CreatedAt: m.now()},
		{ID: 12, TenantID: 1, AgentPrincipalID: 7, Kind: "FULL", State: "REQUESTED", Reason: "Waiting for runner", RequestedBy: 2, CreatedAt: m.now()},
		{ID: 13, TenantID: 2, AgentPrincipalID: 7, Kind: "FULL", State: "DONE", Reason: "Foreign tenant reset", RequestedBy: 2, CreatedAt: m.now()},
	} {
		require.NoError(t, db.Table("k_agents_resets").Create(&row).Error)
	}
	read := func(actor, before guid.ID, limit int) (Page[ResetHistoryView], error) {
		var page Page[ResetHistoryView]
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			page, e = m.resetHistoryOn(agentContext(t, actor), tx, 1, 7, before, limit)
			return e
		})
		return page, err
	}
	page, err := read(2, 0, 2)
	require.NoError(t, err)
	require.True(t, page.HasMore)
	require.Equal(t, "11", page.NextCursor)
	require.Equal(t, "REQUESTED", page.Items[0].State)
	require.Equal(t, "Runner rejected reset", page.Items[1].Reason)
	page, err = read(7, 11, 2)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.EqualValues(t, 10, page.Items[0].ID)
	require.False(t, page.HasMore)
	_, err = read(9, 0, 2)
	require.ErrorIs(t, err, ErrForbidden)
	_, err = read(2, 0, 101)
	require.ErrorIs(t, err, ErrInvalid)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = read(4, 0, 2)
	require.ErrorIs(t, err, ErrForbidden)
	page, err = read(2, 0, 2)
	require.NoError(t, err, "responsible Human retains established workspace visibility")
	require.Len(t, page.Items, 2)
}

func TestResetTimeoutHistoryPreservesEpochsAndRejectsLateSuccess(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	require.NoError(t, m.bindRuntime(RuntimeControl{RequestOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, int, int) error { return nil }}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		reset, err := m.resetOn(ctx, tx, 1, 7, ResetInput{Kind: "SESSION", Reason: "Timed out runner request"})
		if err != nil {
			return err
		}
		require.NoError(t, m.completeResetOn(ctx, tx, 1, 7, reset.ID, "TIMED_OUT"))
		require.ErrorIs(t, m.completeResetOn(ctx, tx, 1, 7, reset.ID, "DONE"), ErrConflict)
		history, err := m.resetHistoryOn(ctx, tx, 1, 7, 0, 10)
		require.NoError(t, err)
		require.Len(t, history.Items, 1)
		require.Equal(t, "TIMED_OUT", history.Items[0].State)
		live, err := m.liveOn(ctx, tx, 1, 7)
		require.NoError(t, err)
		require.Equal(t, 1, live.SessionEpoch)
		require.Equal(t, 1, live.WorkspaceEpoch)
		return nil
	}))
}

func TestResetKindsAdvanceOnlyConfirmedEpochsAndRejectLateCompletion(t *testing.T) {
	for _, kind := range []string{"RESTART", "SESSION", "FULL"} {
		t.Run(kind, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 2)
			m.fullResetProof = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }
			require.ErrorIs(t, m.bindRuntime(RuntimeControl{}), ErrInvalid)
			require.NoError(t, m.bindRuntime(RuntimeControl{RequestOn: func(_ context.Context, _ *gorm.DB, tenant, agent, id guid.ID, k string, session, workspace int) error {
				require.Equal(t, kind, k)
				require.Positive(t, id)
				require.EqualValues(t, 1, tenant)
				require.EqualValues(t, 7, agent)
				if kind == "RESTART" {
					require.Equal(t, 1, session)
				} else {
					require.Equal(t, 2, session)
				}
				if kind == "FULL" {
					require.Equal(t, 2, workspace)
				} else {
					require.Equal(t, 1, workspace)
				}
				return nil
			}}))
			in := ResetInput{Kind: kind, Reason: "Reset confirmed runtime state", ConfirmName: "Test agent", ReauthProof: "proof"}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				require.NoError(t, m.reportOn(ctx, tx, 1, 7, 1, "ONLINE"))
				require.NoError(t, m.reportOn(ctx, tx, 1, 7, 1, "ONLINE"))
				reset, e := m.resetOn(ctx, tx, 1, 7, in)
				require.NoError(t, e)
				require.NoError(t, m.completeResetOn(ctx, tx, 1, 7, reset.ID, "FAILED"))
				require.ErrorIs(t, m.completeResetOn(ctx, tx, 1, 7, reset.ID, "DONE"), ErrConflict)
				before, e := m.liveOn(ctx, tx, 1, 7)
				require.NoError(t, e)
				require.Equal(t, 1, before.SessionEpoch)
				require.Equal(t, 1, before.WorkspaceEpoch)
				reset, e = m.resetOn(ctx, tx, 1, 7, in)
				require.NoError(t, e)
				require.NoError(t, m.completeResetOn(ctx, tx, 1, 7, reset.ID, "DONE"))
				after, e := m.liveOn(ctx, tx, 1, 7)
				require.NoError(t, e)
				if kind == "RESTART" {
					require.Equal(t, 1, after.SessionEpoch)
				} else {
					require.Equal(t, 2, after.SessionEpoch)
					require.ErrorIs(t, m.reportOn(ctx, tx, 1, 7, 1, "ONLINE"), ErrConflict)
				}
				if kind == "FULL" {
					require.Equal(t, 2, after.WorkspaceEpoch)
				} else {
					require.Equal(t, 1, after.WorkspaceEpoch)
				}
				require.Equal(t, "OFFLINE", after.Activity)
				return nil
			}))
		})
	}
}

func TestResetRuntimeFailuresKeepPublicStatusAndNeverWriteReset(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cause, want error
	}{
		{"busy", fleet.ErrVersionConflict, ErrConflict},
		{"offline", fleet.ErrMachineOffline, ErrConflict},
		{"stale", fleet.ErrSessionStale, ErrConflict},
		{"revoked", fleet.ErrMachineRevoked, ErrForbidden},
		{"denied", fleet.ErrMachineForbidden, ErrForbidden},
		{"missing", fleet.ErrMachineNotFound, ErrNotFound},
		{"unavailable", fleet.ErrUnavailable, ErrUnavailable},
	} {
		for _, phase := range []string{"prepare", "enqueue"} {
			t.Run(tc.name+"/"+phase, func(t *testing.T) {
				m, db, _ := agentHarness(t)
				requestCalls := 0
				require.NoError(t, m.bindRuntime(RuntimeControl{
					PrepareOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error {
						if phase == "prepare" {
							return tc.cause
						}
						return nil
					},
					RequestOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, int, int) error {
						requestCalls++
						return tc.cause
					},
				}))
				err := db.Transaction(func(tx *gorm.DB) error {
					_, e := m.resetOn(agentContext(t, 2), tx, 1, 7, ResetInput{Kind: "SESSION", Reason: "Clear stale runtime session"})
					return e
				})
				require.ErrorIs(t, err, tc.want)
				if phase == "prepare" {
					require.Zero(t, requestCalls)
				} else {
					require.Equal(t, 1, requestCalls)
				}
				var count int64
				require.NoError(t, db.Table(m.names.Reset.String()).Count(&count).Error)
				require.Zero(t, count)
			})
		}
	}
}

func TestLifecycleRejectsInvalidReportsAndUnverifiedFullResets(t *testing.T) {
	m, db, _ := agentHarness(t)
	m.fullResetProof = nil
	ctx := agentContext(t, 2)
	require.NoError(t, m.bindRuntime(RuntimeControl{RequestOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, int, int) error {
		return errors.New("runner unavailable")
	}}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, m.reportOn(ctx, tx, 1, 7, 0, "ONLINE"), ErrInvalid)
		require.ErrorIs(t, m.reportOn(ctx, tx, 1, 7, 1, "UNKNOWN"), ErrInvalid)
		require.ErrorIs(t, m.reportOn(ctx, tx, 1, 99, 1, "ONLINE"), ErrNotFound)
		require.ErrorIs(t, m.completeResetOn(ctx, tx, 1, 7, 1, "UNKNOWN"), ErrInvalid)
		_, e := m.resetOn(ctx, tx, 1, 7, ResetInput{Kind: "FULL", Reason: "Clear the stale workspace", ConfirmName: "Test agent", ReauthProof: "proof"})
		require.ErrorIs(t, e, ErrForbidden)
		m.fullResetProof = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
		_, e = m.resetOn(ctx, tx, 1, 7, ResetInput{Kind: "FULL", Reason: "Clear the stale workspace", ConfirmName: "Test agent", ReauthProof: "proof"})
		require.ErrorIs(t, e, ErrForbidden)
		_, e = m.resetOn(ctx, tx, 1, 7, ResetInput{Kind: "RESTART", Reason: "Clear the stale workspace"})
		require.ErrorContains(t, e, "runner unavailable")
		_, e = m.resetOn(ctx, tx, 1, 7, ResetInput{Kind: "UNKNOWN", Reason: "Invalid reset request"})
		require.ErrorIs(t, e, ErrInvalid)
		m.agents.ByPrincipalOn = nil
		require.ErrorIs(t, m.reportOn(ctx, tx, 1, 7, 1, "ONLINE"), ErrUnavailable)
		return nil
	}))
}

func TestResetPersistenceAndRearmFailuresNeverQueuePartialRuntimeCommand(t *testing.T) {
	for _, mode := range []string{"state", "query", "write", "audit", "rearm", "observation"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 2)
			fault := errors.New("reset persistence failed")
			m.runtimeControl.RequestOn = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID, _ string, _, _ int) error {
				return tx.Exec("INSERT INTO audit_probe VALUES ('queued-reset')").Error
			}
			switch mode {
			case "audit":
				m.audit = nil
			case "rearm":
				m.profiles.RearmOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return fault }
			case "state", "query":
				target := m.names.Reset.String()
				if mode == "state" {
					target = m.names.LiveState.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("reset-read", func(tx *gorm.DB) {
					if tx.Statement.Table == target {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Query().Remove("reset-read")
			case "write", "observation":
				target := m.names.Reset.String()
				if mode == "observation" {
					target = m.names.ProcessEvent.String()
				}
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("reset-write", func(tx *gorm.DB) {
					if tx.Statement.Table == target {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Create().Remove("reset-write")
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.resetOn(ctx, tx, 1, 7, ResetInput{Kind: "RESTART", Reason: "Restart actual current process"})
				return e
			}))
			if mode == "state" || mode == "query" {
				require.NoError(t, db.Callback().Query().Remove("reset-read"))
			}
			count, e := query.BindReset(db, m.names).WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, count)
			var effects int64
			require.NoError(t, db.Table("audit_probe").Count(&effects).Error)
			require.Zero(t, effects)
		})
	}
}
