package agents

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementCleanupRequiresRealSourceACKOrTimeoutAndAtomicRevocation(t *testing.T) {
	for _, outcome := range []string{"ACKED", "TIMED_OUT"} {
		t.Run(outcome, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			now := time.Now().UTC().Truncate(time.Millisecond)
			m.now = func() time.Time { return now }
			m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return nil }
			ctx := agentContext(t, 2)
			agent := guid.ID(7)
			r := model.Retirement{ID: 900, TenantID: 1, AgentPrincipalID: agent, ActiveAgentID: &agent, Mode: "NORMAL", State: "IN_PROGRESS", Step: "CLEANUP", Reason: "Finish actual source retirement", AgentVersion: 6, Version: 1, CreatedBy: 2, CreatedAt: now, StepStartedAt: now, Precheck: model.RetirementPrecheck{Progress: &model.RetirementProgress{}}}
			require.NoError(t, query.BindRetirement(db, m.names).WithContext(ctx).Create(&r))
			fleetVersion := 7
			m.profiles.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentProfile, error) {
				return fleet.AgentProfile{PrincipalID: agent, Version: fleetVersion}, nil
			}
			requests, completions, timeouts := 0, 0, 0
			m.retirementRuntime.RequestCleanupOn = func(_ context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int) (fleet.AgentRetirementBinding, error) {
				requests++
				require.Equal(t, fleetVersion, version)
				command := guid.ID(501)
				return fleet.AgentRetirementBinding{PrincipalID: principal, RetirementID: retirement, State: "RETIRING", Version: version + 1, CleanupCommandID: &command}, tx.Exec("INSERT INTO audit_probe VALUES ('cleanup_requested')").Error
			}
			state := "PENDING"
			m.retirementRuntime.CleanupStateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return state, nil }
			finish := func(tx *gorm.DB, principal, retirement guid.ID, version int) (fleet.AgentRetirementBinding, error) {
				require.Equal(t, fleetVersion, version)
				return fleet.AgentRetirementBinding{PrincipalID: principal, RetirementID: retirement, State: "RETIRED", Version: version + 1}, tx.Exec("INSERT INTO audit_probe VALUES ('source_revoked')").Error
			}
			m.retirementRuntime.CompleteOn = func(_ context.Context, tx *gorm.DB, _, principal, retirement guid.ID, version int) (fleet.AgentRetirementBinding, error) {
				completions++
				require.Equal(t, "ACKED", state)
				return finish(tx, principal, retirement, version)
			}
			m.retirementRuntime.CompleteTimedOutOn = func(_ context.Context, tx *gorm.DB, _, principal, retirement guid.ID, version int) (fleet.AgentRetirementBinding, error) {
				timeouts++
				require.False(t, now.Before(r.StepStartedAt.Add(retirementCleanupTimeout)))
				return finish(tx, principal, retirement, version)
			}
			advance := func(agentVersion int) error {
				return db.Transaction(func(tx *gorm.DB) error {
					next, err := m.advanceRetirementCleanupOn(ctx, tx, 1, r.ID, r.Version, agentVersion)
					if err == nil {
						r = next
					}
					return err
				})
			}
			require.ErrorIs(t, advance(6), ErrConflict)
			require.Zero(t, requests)
			require.NoError(t, advance(fleetVersion))
			require.Equal(t, "PENDING", r.Precheck.Progress.CleanupStatus)
			require.Equal(t, "IN_PROGRESS", r.State)
			require.Equal(t, 1, requests)
			fleetVersion++
			version := r.Version
			require.NoError(t, advance(fleetVersion))
			require.Equal(t, version, r.Version)
			require.Zero(t, completions)
			require.Zero(t, timeouts)
			if outcome == "ACKED" {
				state = "ACKED"
				fleetVersion++
			} else {
				now = now.Add(retirementCleanupTimeout)
			}
			auditAppender := m.audit
			m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error {
				return errors.New("retirement audit unavailable")
			}
			require.ErrorContains(t, advance(fleetVersion), "retirement audit unavailable")
			var revoked int64
			require.NoError(t, db.Table("audit_probe").Where("event_type = ?", "source_revoked").Count(&revoked).Error)
			require.Zero(t, revoked, "source revocation and protocol completion roll back together")
			q := query.BindRetirement(db, m.names)
			stored, err := q.WithContext(ctx).Where(q.ID.Eq(r.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "IN_PROGRESS", stored.State)
			require.Equal(t, "PENDING", stored.Precheck.Progress.CleanupStatus)
			m.audit = auditAppender
			require.NoError(t, advance(fleetVersion))
			require.Equal(t, "COMPLETED", r.State)
			require.Equal(t, "DONE", r.Step)
			require.Nil(t, r.ActiveAgentID)
			require.NotNil(t, r.CompletedAt)
			require.Equal(t, outcome, r.Precheck.Progress.CleanupStatus)
			require.ErrorIs(t, advance(fleetVersion), ErrConflict)
		})
	}
}

func TestRetirementCleanupProviderFailuresNeverMarkRuntimeRevoked(t *testing.T) {
	for _, mode := range []string{"missing-lock", "lock", "missing-runtime", "profile", "request", "bad-binding", "state", "unknown-state", "complete", "bad-completion"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 2)
			agent := guid.ID(7)
			fault := errors.New("cleanup source unavailable")
			progress := &model.RetirementProgress{}
			if mode == "state" || mode == "unknown-state" || mode == "complete" || mode == "bad-completion" {
				progress.CleanupStatus = "PENDING"
			}
			r := model.Retirement{ID: 900, TenantID: 1, AgentPrincipalID: agent, ActiveAgentID: &agent, Mode: "NORMAL", State: "IN_PROGRESS", Step: "CLEANUP", Reason: "Clean current runtime resources", AgentVersion: 6, Version: 1, CreatedBy: 2, CreatedAt: m.now(), StepStartedAt: m.now(), Precheck: model.RetirementPrecheck{Progress: progress}}
			require.NoError(t, query.BindRetirement(db, m.names).WithContext(ctx).Create(&r))
			m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return nil }
			m.profiles.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentProfile, error) {
				return fleet.AgentProfile{PrincipalID: 7, Version: 6}, nil
			}
			m.retirementRuntime.RequestCleanupOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (fleet.AgentRetirementBinding, error) {
				return fleet.AgentRetirementBinding{}, fault
			}
			m.retirementRuntime.CleanupStateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return "ACKED", nil }
			m.retirementRuntime.CompleteOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (fleet.AgentRetirementBinding, error) {
				return fleet.AgentRetirementBinding{}, fault
			}
			m.retirementRuntime.CompleteTimedOutOn = m.retirementRuntime.CompleteOn
			switch mode {
			case "missing-lock":
				m.principals.Lock = nil
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return fault }
			case "missing-runtime":
				m.retirementRuntime.CompleteOn = nil
			case "profile":
				m.profiles.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentProfile, error) {
					return fleet.AgentProfile{}, fault
				}
			case "bad-binding":
				m.retirementRuntime.RequestCleanupOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (fleet.AgentRetirementBinding, error) {
					return fleet.AgentRetirementBinding{}, nil
				}
			case "state":
				m.retirementRuntime.CleanupStateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return "", fault }
			case "unknown-state":
				m.retirementRuntime.CleanupStateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return "UNKNOWN", nil }
			case "bad-completion":
				m.retirementRuntime.CompleteOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (fleet.AgentRetirementBinding, error) {
					return fleet.AgentRetirementBinding{State: "RETIRED"}, nil
				}
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceRetirementCleanupOn(ctx, tx, 1, r.ID, 1, 6); return e }))
			q := query.BindRetirement(db, m.names)
			stored, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, "IN_PROGRESS", stored.State)
			require.Equal(t, "CLEANUP", stored.Step)
			require.Equal(t, 1, stored.Version)
			require.Nil(t, stored.CompletedAt)
		})
	}
}
