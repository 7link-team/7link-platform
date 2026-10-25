package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestRetirementForcedOfflineDefersCleanupAndRollsBackRevocationFailures(t *testing.T) {
	for _, failure := range []string{"", "revoker", "account", "observer", "audit", "outbox", "stale", "authority"} {
		t.Run(failure, func(t *testing.T) {
			f := agentRebindHarness(t)
			require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 1).Update("state", "OFFLINE").Error)
			version := 1
			revoke := func(_ context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID) error {
				require.EqualValues(t, 901, retirement)
				if failure == "revoker" {
					return errors.New("revoker failed")
				}
				return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES (?)", "forced_bindings_revoked").Error
			}
			f.m.revokeAccount = func(_ context.Context, tx *gorm.DB, tenant, principal guid.ID) error {
				if failure == "account" {
					return errors.New("account failed")
				}
				return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES (?)", "forced_account_disabled").Error
			}
			f.m.retirementObserver = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error {
				if failure == "observer" {
					return errors.New("observer failed")
				}
				return nil
			}
			previousAudit := f.m.audit
			f.m.audit = func(ctx context.Context, tx *gorm.DB, e audit.DomainEvent) error {
				if failure == "audit" {
					return errors.New("audit failed")
				}
				if e.EventType == "fleet.agent_retired" {
					require.Equal(t, "FORCED", e.Detail["mode"])
					require.Equal(t, "PENDING", e.Detail["cleanup_status"])
					require.Equal(t, RetirementLoss{OpenItems: 2, RunningExecutions: 1}, e.Detail["lost"])
				}
				return previousAudit(ctx, tx, e)
			}
			if failure == "outbox" {
				f.publisher.fail = true
			}
			if failure == "stale" {
				version = 9
			}
			if failure == "authority" {
				f.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			}
			var binding AgentRetirementBinding
			err := f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				binding, e = f.m.forceAgentRetirementOn(f.human, tx, 7, 202, 901, version, f.agent(t).Name, RetirementLoss{OpenItems: 2, RunningExecutions: 1}, revoke)
				return e
			})
			if failure != "" {
				require.Error(t, err)
				require.NotEqual(t, "RETIRED", f.agent(t).State)
				require.NotNil(t, f.agent(t).MachineID)
				var effects int64
				require.NoError(t, f.db.Table("test_fleet_effects").Where("kind IN ?", []string{"forced_bindings_revoked", "forced_account_disabled"}).Count(&effects).Error)
				require.Zero(t, effects)
				commands := query.BindAgentMachineCommands(f.db, f.m.names)
				count, e := commands.WithContext(t.Context()).Where(commands.Kind.Eq("CLEANUP")).Count()
				require.NoError(t, e)
				require.Zero(t, count)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "RETIRED", binding.State)
			require.NotNil(t, binding.CleanupCommandID)
			require.Nil(t, f.agent(t).MachineID)
			require.EqualValues(t, 1, *f.agent(t).PendingCleanupMachineID)
			require.EqualValues(t, 901, *f.agent(t).RetirementID)
			commands := query.BindAgentMachineCommands(f.db, f.m.names)
			command, e := commands.WithContext(t.Context()).Where(commands.ID.Eq(*binding.CleanupCommandID)).First()
			require.NoError(t, e)
			require.Equal(t, "QUEUED", command.State)
			require.Equal(t, 30*24*time.Hour, command.ExpiresAt.Sub(command.CreatedAt))
			// The real machine/session protocol delivers deferred cleanup and alone
			// establishes its ACK. Retirement itself did not claim process exit.
			require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 1).Update("state", "ONLINE").Error)
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				items, e := f.m.pullAgentMachineCommandsOn(f.oldRunner, tx, 7, 1, f.oldSession.ID, f.oldSession.ConnEpoch)
				if e != nil {
					return e
				}
				require.Len(t, items, 1)
				_, e = f.m.reportAgentMachineCommandOn(f.oldRunner, tx, 7, 1, items[0].ID, f.oldSession.ID, f.oldSession.ConnEpoch, "DONE", "")
				return e
			}))
			require.Nil(t, f.agent(t).PendingCleanupMachineID)
		})
	}
}

func TestRetirementCleanupTimeoutPreservesPendingUntilActualLateMachineACK(t *testing.T) {
	f := agentRebindHarness(t)
	require.NoError(t, f.db.Table(f.m.names.Agents.String()).Where("principal_id=?", 202).Update("process_status", "RUNNING").Error)
	var binding AgentRetirementBinding
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		binding, err = f.m.beginAgentRetirementOn(f.human, tx, 7, 202, 901, 1)
		if err != nil {
			return err
		}
		binding, err = f.m.requestAgentRetirementCleanupOn(f.human, tx, 7, 202, 901, binding.Version, true)
		return err
	}))
	require.Equal(t, "STOPPING", f.agent(t).ProcessStatus)
	require.NotNil(t, binding.CleanupCommandID)
	require.NotNil(t, f.agent(t).PendingCleanupMachineID)
	f.m.revokeAccount = func(_ context.Context, tx *gorm.DB, _, _ guid.ID) error {
		return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('timeout_credentials')").Error
	}
	f.m.retirementObserver = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	fail := false
	revoke := func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID) error {
		if fail {
			return errors.New("revocation unavailable")
		}
		return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('timeout_bindings')").Error
	}
	complete := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			var err error
			binding, err = f.m.completeTimedOutRetirementOn(f.human, tx, 7, 202, 901, f.agent(t).Version, revoke)
			return err
		})
	}
	require.ErrorIs(t, complete(), ErrAgentRebindState, "caller cannot assert an elapsed cleanup timeout")
	q := query.BindAgentMachineCommands(f.db, f.m.names)
	_, err := q.WithContext(t.Context()).Where(q.AgentPrincipalID.Eq(guid.ID(202)), q.Kind.Eq("CLEANUP")).Update(q.CreatedAt, time.Now().UTC().Add(-5*time.Minute-time.Second))
	require.NoError(t, err)
	fail = true
	require.ErrorContains(t, complete(), "revocation unavailable")
	require.Equal(t, "RETIRING", f.agent(t).State)
	require.NotNil(t, f.agent(t).PendingCleanupMachineID)
	fail = false
	require.NoError(t, complete())
	require.Equal(t, "RETIRED", f.agent(t).State)
	require.Equal(t, "STOPPING", f.agent(t).ProcessStatus, "timeout does not invent process exit")
	require.NotNil(t, f.agent(t).PendingCleanupMachineID)
	commands, err := q.WithContext(t.Context()).Where(q.AgentPrincipalID.Eq(guid.ID(202)), q.Kind.Eq("CLEANUP")).Find()
	require.NoError(t, err)
	require.Len(t, commands, 1)
	require.Equal(t, "QUEUED", commands[0].State, "timeout leaves real cleanup eligible on reconnect")
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		commands, err := f.m.pullAgentMachineCommandsOn(f.oldRunner, tx, 7, 1, f.oldSession.ID, f.oldSession.ConnEpoch)
		if err != nil {
			return err
		}
		require.Len(t, commands, 1)
		_, err = f.m.reportAgentMachineCommandOn(f.oldRunner, tx, 7, 1, commands[0].ID, f.oldSession.ID, f.oldSession.ConnEpoch, "DONE", "")
		return err
	}))
	require.Equal(t, "RETIRED", f.agent(t).State)
	require.Equal(t, "EXITED", f.agent(t).ProcessStatus)
	require.Nil(t, f.agent(t).PendingCleanupMachineID)
}

func TestRetirementRuntimeFreezesDispatchAndRequiresActualCleanupACK(t *testing.T) {
	f := agentRebindHarness(t)
	retirement := guid.ID(901)
	var binding AgentRetirementBinding
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		binding, err = f.m.beginAgentRetirementOn(f.human, tx, 7, 202, retirement, 1)
		return err
	}))
	require.Equal(t, "RETIRING", binding.State)
	require.Equal(t, 2, binding.Version)
	require.Equal(t, retirement, *f.agent(t).RetirementID)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		a, found, err := f.m.ByPrincipalOn(f.human, tx, 7, 202)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "RETIRING", a.State, "dispatch reads the actual frozen state")
		binding, err = f.m.cleanupAgentRetirementOn(f.human, tx, 7, 202, retirement, 2)
		return err
	}))
	require.NotNil(t, binding.CleanupCommandID)
	require.Nil(t, f.agent(t).MachineID)
	require.EqualValues(t, 1, *f.agent(t).PendingCleanupMachineID)
	check := func(want string) {
		t.Helper()
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			state, err := f.m.agentRetirementCleanupStateOn(f.human, tx, 7, 202, retirement)
			require.NoError(t, err)
			require.Equal(t, want, state)
			return nil
		}))
	}
	check("PENDING")
	// A receipt for a command never pulled by this machine cannot establish ACK.
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.reportAgentMachineCommandOn(f.oldRunner, tx, 7, 1, *binding.CleanupCommandID, f.oldSession.ID, f.oldSession.ConnEpoch, "DONE", "")
		return err
	}), ErrSessionStale)
	check("PENDING")
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		commands, err := f.m.pullAgentMachineCommandsOn(f.oldRunner, tx, 7, 1, f.oldSession.ID, f.oldSession.ConnEpoch)
		require.NoError(t, err)
		require.Len(t, commands, 1)
		require.Equal(t, *binding.CleanupCommandID, commands[0].ID)
		require.Equal(t, "CLEANUP", commands[0].Kind)
		require.Empty(t, commands[0].Secret)
		return nil
	}))
	check("PENDING")
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.reportAgentMachineCommandOn(f.oldRunner, tx, 7, 1, *binding.CleanupCommandID, f.oldSession.ID, f.oldSession.ConnEpoch+1, "DONE", "")
		return err
	}), ErrSessionStale)
	check("PENDING")
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.reportAgentMachineCommandOn(f.oldRunner, tx, 7, 1, *binding.CleanupCommandID, f.oldSession.ID, f.oldSession.ConnEpoch, "DONE", "")
		return err
	}))
	check("ACKED")
	require.Equal(t, "RETIRING", f.agent(t).State, "cleanup alone cannot claim final revocation")
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.cancelAgentRetirementOn(f.human, tx, 7, 202, retirement, f.agent(t).Version)
		return err
	}), ErrVersionConflict)
	// All source-owner side effects are forced into the same transaction. A
	// Knowledge failure must roll back the prior IAM/role/Fleet changes.
	f.m.revokeAccount = func(_ context.Context, tx *gorm.DB, tenant, principal guid.ID) error {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 202, principal)
		return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES (?)", "retirement_credentials_revoked").Error
	}
	f.m.retirementObserver = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error {
		return errors.New("knowledge freeze unavailable")
	}
	version := f.agent(t).Version
	require.ErrorContains(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.completeAgentRetirementOn(f.human, tx, 7, 202, retirement, version)
		return err
	}), "knowledge freeze unavailable")
	require.Equal(t, "RETIRING", f.agent(t).State)
	var revoked int64
	require.NoError(t, f.db.Table("test_fleet_effects").Where("kind=?", "retirement_credentials_revoked").Count(&revoked).Error)
	require.Zero(t, revoked)
	roles := query.BindAgentRoles(f.db, f.m.names)
	active, err := roles.WithContext(t.Context()).Where(roles.AgentID.Eq(f.agent(t).ID), roles.RevokedAt.IsNull()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, active)
	f.m.retirementObserver = func(_ context.Context, tx *gorm.DB, tenant, principal, event guid.ID) error {
		require.Positive(t, event)
		return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES (?)", "knowledge_agent_frozen").Error
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		binding, err = f.m.completeAgentRetirementOn(f.human, tx, 7, 202, retirement, version)
		return err
	}))
	require.Equal(t, "RETIRED", binding.State)
	require.Equal(t, "FAILED", f.agent(t).CredentialDelivery)
	require.False(t, f.agent(t).AutoRestart)
	active, err = roles.WithContext(t.Context()).Where(roles.AgentID.Eq(f.agent(t).ID), roles.RevokedAt.IsNull()).Count()
	require.NoError(t, err)
	require.Zero(t, active)
	require.NoError(t, f.db.Table("test_fleet_effects").Where("kind=?", "retirement_credentials_revoked").Count(&revoked).Error)
	require.EqualValues(t, 1, revoked)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.completeAgentRetirementOn(f.human, tx, 7, 202, retirement, version)
		return err
	}), ErrVersionConflict)
}

func TestRetirementRuntimeCancellationAndAllFailureFencesPreserveBinding(t *testing.T) {
	for _, fault := range []string{"authority", "foreign_tenant", "nonhuman", "stale", "offline", "outbox", "audit"} {
		t.Run("begin/"+fault, func(t *testing.T) {
			f := agentRebindHarness(t)
			ctx, version := f.human, 1
			switch fault {
			case "authority":
				f.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "foreign_tenant":
				ctx = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 8, PrincipalID: 11})
			case "nonhuman":
				f.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "stale":
				version = 5
			case "offline":
				require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 1).Update("state", "OFFLINE").Error)
			case "outbox":
				f.publisher.fail = true
			case "audit":
				f.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			before := f.agent(t)
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, err := f.m.beginAgentRetirementOn(ctx, tx, 7, 202, 901, version)
				return err
			}))
			require.Equal(t, before, f.agent(t))
		})
	}
	f := agentRebindHarness(t)
	require.ErrorIs(t, func() error { _, err := f.m.beginAgentRetirementOn(f.human, f.db, 7, 202, 901, 1); return err }(), ErrInvalidArgument)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.beginAgentRetirementOn(f.human, tx, 7, 202, 901, 1); return err }))
	for _, fault := range []string{"retirement", "version", "running", "execution", "outbox", "audit"} {
		t.Run("cleanup/"+fault, func(t *testing.T) {
			retirement, version := guid.ID(901), 2
			originalAudit := f.m.audit
			switch fault {
			case "retirement":
				retirement = 902
			case "version":
				version = 1
			case "running":
				require.NoError(t, f.db.Table(f.m.names.Agents.String()).Where("principal_id=?", 202).Update("process_status", "RUNNING").Error)
			case "execution":
				f.m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil }
			case "outbox":
				f.publisher.fail = true
			case "audit":
				f.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			before := f.agent(t)
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, err := f.m.cleanupAgentRetirementOn(f.human, tx, 7, 202, retirement, version)
				return err
			}))
			require.Equal(t, before, f.agent(t))
			q := query.BindAgentMachineCommands(f.db, f.m.names)
			n, err := q.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n, "failed cleanup must not leave an actionable machine command")
			f.publisher.fail, f.m.audit = false, originalAudit
			f.m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }
			require.NoError(t, f.db.Table(f.m.names.Agents.String()).Where("principal_id=?", 202).Update("process_status", "NONE").Error)
		})
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.cancelAgentRetirementOn(f.human, tx, 7, 202, 901, 2)
		return err
	}))
	require.Equal(t, "STOPPED", f.agent(t).State)
	require.Nil(t, f.agent(t).RetirementID)
	require.NotNil(t, f.agent(t).MachineID)
}

func TestRetirementRuntimeComposedRevokersAreRequiredAtomicAndSupportSpawnedChildren(t *testing.T) {
	f := agentRebindHarness(t)
	require.NoError(t, f.db.Table(f.m.names.Agents.String()).Where("principal_id=?", 202).Updates(map[string]any{"origin": "AGENT_SPAWNED", "parent_agent_id": 800, "spawn_grant_id": 801, "spawn_reason": "Bounded spawned worker"}).Error)
	var binding AgentRetirementBinding
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		binding, err = f.m.beginAgentRetirementOn(f.human, tx, 7, 202, 901, 1)
		if err != nil {
			return err
		}
		binding, err = f.m.cleanupAgentRetirementOn(f.human, tx, 7, 202, 901, binding.Version)
		return err
	}))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		commands, err := f.m.pullAgentMachineCommandsOn(f.oldRunner, tx, 7, 1, f.oldSession.ID, f.oldSession.ConnEpoch)
		require.NoError(t, err)
		require.Len(t, commands, 1)
		_, err = f.m.reportAgentMachineCommandOn(f.oldRunner, tx, 7, 1, commands[0].ID, f.oldSession.ID, f.oldSession.ConnEpoch, "DONE", "")
		return err
	}))
	f.m.revokeAccount = func(_ context.Context, tx *gorm.DB, _, _ guid.ID) error {
		return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('credentials')").Error
	}
	f.m.retirementObserver = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	s := module.NewServices()
	require.NoError(t, f.m.provideWorkspace(s))
	require.NoError(t, s.Seal())
	runtime, err := module.Resolve[AgentRetirementRuntime](s)
	require.NoError(t, err)
	c, err := module.Resolve[AgentRetirementRevocationConnector](s)
	require.NoError(t, err)
	binding.Version = f.agent(t).Version // the actual cleanup ACK advances Fleet's fence
	complete := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			_, err := runtime.CompleteOn(f.human, tx, 7, 202, 901, binding.Version)
			return err
		})
	}
	require.ErrorIs(t, complete(), ErrUnavailable)
	revoke := func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID) error {
		return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('governance')").Error
	}
	require.NoError(t, c.Bind("business_governance", revoke))
	require.Error(t, c.Bind("business_governance", revoke))
	require.ErrorIs(t, complete(), ErrUnavailable, "missing channel revoker cannot be silently ignored")
	fail := true
	require.NoError(t, c.Bind("collab", func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID) error {
		if fail {
			return errors.New("channel revocation unavailable")
		}
		return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('channels')").Error
	}))
	require.ErrorContains(t, complete(), "channel revocation unavailable")
	var effects int64
	require.NoError(t, f.db.Table("test_fleet_effects").Where("kind IN ?", []string{"governance", "channels", "credentials"}).Count(&effects).Error)
	require.Zero(t, effects, "Governance and credential writes must roll back with channel failure")
	require.Equal(t, "RETIRING", f.agent(t).State)
	fail = false
	require.NoError(t, complete())
	require.Equal(t, "RETIRED", f.agent(t).State)
	require.NoError(t, f.db.Table("test_fleet_effects").Where("kind IN ?", []string{"governance", "channels", "credentials"}).Count(&effects).Error)
	require.EqualValues(t, 3, effects)
}

func TestAtomicAgentProvisioningQueuesEncryptedCredentialAndRequiresRealACK(t *testing.T) {
	m, db, human, runner, session, _, in := agentRegistrationHarness(t)
	m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	// New identities have responsibility roles, never preexisting tenant grants.
	m.agentRoles = func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) error { return ErrMachineForbidden }
	secret := identity.ServiceAccountCredentialSecret{Credential: identity.ServiceAccountCredential{ID: 303, ServiceAccountID: 202}, Secret: "private-registration-secret"}
	style, seed, policy := "MONSTER", int64(42), json.RawMessage(`{"profile":"STANDARD"}`)
	var registered RegisteredAgent
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		registered, e = m.provisionAgentOn(human, tx, 7, in, AgentProfilePatch{AvatarStyle: &style, AvatarSeed: &seed, MemoryPolicy: &policy}, secret)
		return e
	}))
	q := query.BindAgents(db, m.names)
	a, e := q.WithContext(t.Context()).Where(q.ID.Eq(registered.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "MONSTER", a.AvatarStyle)
	require.EqualValues(t, 42, a.AvatarSeed)
	require.Equal(t, "PENDING", a.CredentialDelivery)
	require.False(t, agentBindingReady(a))
	commands, e := query.BindAgentMachineCommands(db, m.names).WithContext(t.Context()).Find()
	require.NoError(t, e)
	require.Len(t, commands, 1)
	require.NotContains(t, commands[0].SealedCredential, secret.Secret)
	var deliveries []AgentMachineDelivery
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		deliveries, e = m.pullAgentMachineCommandsOn(runner, tx, 7, 1, session.ID, session.ConnEpoch)
		return e
	}))
	require.Len(t, deliveries, 1)
	require.Equal(t, secret.Secret, deliveries[0].Secret)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.reportAgentMachineCommandOn(runner, tx, 7, 1, deliveries[0].ID, session.ID, session.ConnEpoch, "DONE", "")
		return e
	}))
	a, e = q.WithContext(t.Context()).Where(q.ID.Eq(registered.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "DELIVERED", a.CredentialDelivery)
	require.True(t, agentBindingReady(a))
}

func TestAtomicAgentProvisioningFailureRollsBackRolesProfileAndDelivery(t *testing.T) {
	for _, fault := range []string{"profile", "outbox", "delivery"} {
		t.Run(fault, func(t *testing.T) {
			m, db, human, _, _, p, in := agentRegistrationHarness(t)
			m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
			baseline := map[string]int64{}
			for _, table := range []string{m.names.Agents.String(), m.names.AgentRoles.String()} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				baseline[table] = count
			}
			style := "PIXEL"
			if fault == "profile" {
				style = "INVALID"
			}
			if fault == "outbox" {
				p.fail = true
			}
			if fault == "delivery" {
				require.NoError(t, db.Migrator().DropTable(m.names.AgentMachineCommands.String()))
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.provisionAgentOn(human, tx, 7, in, AgentProfilePatch{AvatarStyle: &style}, identity.ServiceAccountCredentialSecret{Credential: identity.ServiceAccountCredential{ID: 303, ServiceAccountID: 202}, Secret: "never-in-receipt"})
				return e
			})
			require.Error(t, err)
			for _, table := range []string{m.names.Agents.String(), m.names.AgentRoles.String()} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				require.Equal(t, baseline[table], count)
			}
		})
	}
}

func TestToolSourceFactsUsesObservedCapabilitiesAndCurrentAuthority(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	services := module.NewServices()
	require.NoError(t, m.provideToolSources(services))
	require.NoError(t, services.Seal())
	facts, err := module.Resolve[ToolSourceFacts](services)
	require.NoError(t, err)
	require.NotNil(t, facts.ReadOn)
	_, err = facts.ReadOn(ctx, db, 7, 1)
	require.ErrorIs(t, err, ErrInvalidArgument)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		session, err := m.OpenSessionOn(ctx, tx, 7, 1, "tool-source")
		require.NoError(t, err)
		require.NoError(t, m.RecordInventoryOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch, Inventory{Tools: []ToolObservation{{Name: "node", Version: "24", ExecutablePath: "/observed/node"}}}))
		discovery := discoveryFacts()
		discovery[0].Models = nil
		discovery[0].MCP = []string{"observed-reader"}
		_, err = m.RecordDiscoveryOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch, "CAPABILITIES", discovery)
		require.NoError(t, err)
		return nil
	}))
	m.policy = func(_ context.Context, _ *gorm.DB, tenant guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, "machine_read", permission)
		require.Positive(t, actor)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		got, err := facts.ReadOn(ctx, tx, 7, 1)
		require.NoError(t, err)
		require.Len(t, got.Tools, 1)
		require.Equal(t, []MachineToolView{{Name: "node", Version: "24", Path: "/observed/node", InspectedAt: got.Tools[0].InspectedAt}}, got.Tools)
		require.Len(t, got.Discoveries, 1)
		require.Equal(t, []string{"observed-reader"}, got.Discoveries[0].Discovery[0].MCP)
		for _, tenant := range []guid.ID{0, 8} {
			got, err = facts.ReadOn(ctx, tx, tenant, 1)
			require.Error(t, err)
			require.Empty(t, got)
		}
		got, err = facts.ReadOn(ctx, tx, 7, 999)
		require.ErrorIs(t, err, ErrMachineNotFound)
		require.Empty(t, got)
		return nil
	}))

	for _, failure := range []string{"permission", "principal", "policy_error", "query"} {
		t.Run(failure, func(t *testing.T) {
			originalPolicy := m.policy
			originalPrincipal := m.principals.ActiveHumanByID
			t.Cleanup(func() { m.policy = originalPolicy; m.principals.ActiveHumanByID = originalPrincipal })
			switch failure {
			case "permission":
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "principal":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "policy_error":
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, errors.New("policy unavailable")
				}
			case "query":
				require.NoError(t, db.Migrator().DropTable(m.names.Tools.String()))
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				got, err := facts.ReadOn(ctx, tx, 7, 1)
				require.Error(t, err)
				require.Empty(t, got, "failed reads must not publish partial observations")
				return nil
			}))
		})
	}
}

func TestAgentProfileDoesNotReuseOlderSuccessAfterCurrentInspectionFailure(t *testing.T) {
	m, db, human, _, session, _, in := agentRegistrationHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.RegisterAgentOn(human, tx, 7, in); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		q := query.BindInspections(tx, m.names)
		at := time.Now().UTC()
		require.NoError(t, q.WithContext(t.Context()).Create(&model.Inspection{ID: 90, TenantID: 7, MachineID: 1, RequestedBy: 11, Kind: "MODELS", State: "READY", ScopeKey: "home", SessionID: session.ID, ConnEpoch: session.ConnEpoch, ResultJSON: `[{"runtime":"CLAUDE_CODE","state":"READY","reasoning":[{"model":"sonnet","efforts":["high"]}]}]`, ResultHash: "success", CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(time.Hour)}))
		before, e := m.agentProfileOn(human, tx, 7, 202)
		require.NoError(t, e)
		effort := "high"
		after, e := m.updateAgentProfileOn(human, tx, 7, 202, AgentProfilePatch{Version: before.Version, ReasoningEffort: &effort})
		require.NoError(t, e)
		require.NoError(t, q.WithContext(t.Context()).Create(&model.Inspection{ID: 91, TenantID: 7, MachineID: 1, RequestedBy: 11, Kind: "MODELS", State: "ERROR", ScopeKey: "home", SessionID: session.ID, ConnEpoch: session.ConnEpoch, ResultJSON: `[]`, ResultHash: "failure", CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(time.Hour)}))
		_, e = m.updateAgentProfileOn(human, tx, 7, 202, AgentProfilePatch{Version: after.Version, ReasoningEffort: &effort})
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
}

func TestWorkspaceBindingRequiresTransactionAndActualBoundMachine(t *testing.T) {
	m, db := directoryHarness(t)
	_, e := m.agentWorkspaceBindingOn(t.Context(), db, 7, 21)
	require.ErrorIs(t, e, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.agentWorkspaceBindingOn(t.Context(), tx, 7, guid.ID(999))
		require.ErrorIs(t, err, ErrMachineNotFound)
		return nil
	}))
}

func TestFleetRosterUsesCurrentRoleFiltersAndTenantBounds(t *testing.T) {
	m, db := directoryHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, filter := range []FleetRosterFilter{{Search: "Agent", Role: "EXECUTOR", State: "DRAFT", Runtime: "CODEX", Model: "model", Origin: "HUMAN_CREATED", Lineage: "HUMAN_CREATED"}, {}} {
			page, e := m.fleetRosterOn(t.Context(), tx, 7, filter, 1, 0)
			require.NoError(t, e)
			require.Len(t, page.Items, 1)
			require.Equal(t, guid.ID(101), page.Items[0].PrincipalID)
			require.False(t, page.Items[0].MachineOnline)
		}
		for _, filter := range []FleetRosterFilter{{Role: "LEAD"}, {MachineID: 99}, {ParentAgentID: 99}, {Origin: "AGENT_SPAWNED"}} {
			page, e := m.fleetRosterOn(t.Context(), tx, 7, filter, 50, 0)
			require.NoError(t, e)
			require.Empty(t, page.Items)
		}
		page, e := m.fleetRosterOn(t.Context(), tx, 8, FleetRosterFilter{}, 50, 0)
		require.NoError(t, e)
		require.Empty(t, page.Items)
		_, e = m.fleetRosterOn(t.Context(), tx, 7, FleetRosterFilter{}, 201, 0)
		require.ErrorIs(t, e, ErrInvalidArgument)
		return nil
	}))
}

func TestAgentProfilePatchFencesVersionAndPreservesPrivateConfiguration(t *testing.T) {
	m, db := directoryHarness(t)
	name, persona, prompt, style := "Renamed", "Careful reviewer", "Private revised prompt", "ROBOT"
	seed := int64(9007199254740991)
	parallel := 4
	restart := false
	tags, channels := []string{"frontend", "review"}, []string{"design"}
	var version int
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		before, e := m.agentProfileOn(t.Context(), tx, 7, 101)
		require.NoError(t, e)
		version = before.Version
		after, e := m.updateAgentProfileOn(t.Context(), tx, 7, 101, AgentProfilePatch{Version: version, Name: &name, Persona: &persona, SystemPrompt: &prompt, AvatarStyle: &style, AvatarSeed: &seed, MaxParallelItems: &parallel, AutoRestart: &restart, CapabilityTags: &tags, DefaultChannelNames: &channels})
		require.NoError(t, e)
		require.Equal(t, prompt, after.SystemPrompt)
		require.Equal(t, persona, *after.Persona)
		require.Equal(t, seed, after.AvatarSeed)
		require.Equal(t, tags, after.CapabilityTags)
		require.Equal(t, channels, after.DefaultChannelNames)
		require.Equal(t, before.ConfigRevision+1, after.ConfigRevision)
		require.Equal(t, version+1, after.Version)
		require.False(t, after.AutoRestart)
		require.Equal(t, 4, after.MaxParallelItems)
		_, e = m.updateAgentProfileOn(t.Context(), tx, 7, 101, AgentProfilePatch{Version: version, Name: &name})
		require.ErrorIs(t, e, ErrVersionConflict)
		_, e = m.agentProfileOn(t.Context(), tx, 8, 101)
		require.ErrorIs(t, e, gorm.ErrRecordNotFound)
		return nil
	}))
	abort := errors.New("rollback")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.updateAgentProfileOn(t.Context(), tx, 7, 101, AgentProfilePatch{Version: version + 1, Name: &prompt})
		require.NoError(t, e)
		return abort
	}), abort)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.agentProfileOn(t.Context(), tx, 7, 101)
		require.NoError(t, e)
		require.Equal(t, name, v.Name)
		return nil
	}))
}

func TestAgentProfileRejectsInvalidValuesWithoutChangingRevision(t *testing.T) {
	m, db := directoryHarness(t)
	badTag := []string{"Admin"}
	tooMany := 5
	smallContext := 3999
	badSeed := int64(-1)
	invalidStyle := "CUSTOM"
	unknownMCP := []string{"unobserved-server"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		before, e := m.agentProfileOn(context.Background(), tx, 7, 101)
		require.NoError(t, e)
		for _, patch := range []AgentProfilePatch{{CapabilityTags: &badTag}, {MaxParallelItems: &tooMany}, {MaxContextTokens: &smallContext}, {AvatarSeed: &badSeed}, {AvatarStyle: &invalidStyle}, {MCPAllowlist: &unknownMCP}} {
			patch.Version = before.Version
			_, e = m.updateAgentProfileOn(t.Context(), tx, 7, 101, patch)
			require.Error(t, e)
		}
		after, e := m.agentProfileOn(t.Context(), tx, 7, 101)
		require.NoError(t, e)
		require.Equal(t, before, after)
		return nil
	}))
	_, e := m.agentProfileOn(t.Context(), db, 7, 101)
	require.ErrorIs(t, e, ErrInvalidArgument)
}

func TestAgentProfilePatchCannotStrandPendingCredentialDelivery(t *testing.T) {
	m, db := directoryHarness(t)
	q := query.BindAgents(db, m.names)
	credential := guid.ID(91)
	_, e := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(101))).Updates(map[string]any{"credential_id": credential, "credential_delivery": "PENDING"})
	require.NoError(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		before, err := m.agentProfileOn(t.Context(), tx, 7, 101)
		require.NoError(t, err)
		name := "must not persist"
		_, err = m.updateAgentProfileOn(t.Context(), tx, 7, 101, AgentProfilePatch{Version: before.Version, Name: &name})
		require.ErrorIs(t, err, ErrVersionConflict)
		after, err := m.agentProfileOn(t.Context(), tx, 7, 101)
		require.NoError(t, err)
		require.Equal(t, before, after, "pending delivery revision and profile must remain intact")
		return nil
	}))
}

func TestAgentRunStateUsesActualBindingAndTransactionalProcessObserver(t *testing.T) {
	for _, failure := range []string{"", "query", "machine", "update", "observer", "busy", "quiet-source", "offline", "no-quiet"} {
		t.Run(failure, func(t *testing.T) {
			f := agentRebindHarness(t)
			before := f.agent(t)
			injected := errors.New("run state dependency unavailable")
			observerCalls := 0
			f.m.processObserver = func(_ context.Context, tx *gorm.DB, transition AgentProcessTransition) error {
				observerCalls++
				require.Equal(t, "START_REQUESTED", transition.Kind)
				require.EqualValues(t, 202, transition.AgentPrincipalID)
				if failure == "observer" {
					return injected
				}
				return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('process_start_requested')").Error
			}
			switch failure {
			case "query", "machine", "update":
				table := f.m.names.Agents.String()
				if failure == "machine" {
					table = f.m.names.Machines.String()
				}
				hook := func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(injected)
					}
				}
				if failure == "update" {
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("run_state_failure", hook))
				} else {
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("run_state_failure", hook))
				}
			case "busy":
				f.m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil }
			case "quiet-source":
				f.m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, injected }
			case "no-quiet":
				f.m.agentRebindQuiet = nil
			case "offline":
				require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=1").Update("state", "OFFLINE").Error)
			}
			var state AgentRunState
			e := f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				state, e = f.m.agentRunStateOn(f.human, tx, 7, 202, "ACTIVE")
				return e
			})
			if failure != "" {
				require.Error(t, e)
				require.NoError(t, f.db.Callback().Query().Remove("run_state_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("run_state_failure"))
				require.Equal(t, before.State, f.agent(t).State)
				require.Equal(t, before.Version, f.agent(t).Version)
				var effects int64
				require.NoError(t, f.db.Table("test_fleet_effects").Where("kind='process_start_requested'").Count(&effects).Error)
				require.Zero(t, effects)
				return
			}
			require.NoError(t, e)
			require.Equal(t, "ACTIVE", state.State)
			require.Equal(t, "STARTING", state.ProcessStatus)
			require.Equal(t, 1, observerCalls)
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.agentRunStateOn(f.human, tx, 7, 202, "ACTIVE"); return e }))
			require.Equal(t, 1, observerCalls, "same desired state is a replay")
			f.m.processObserver = func(_ context.Context, _ *gorm.DB, transition AgentProcessTransition) error {
				require.Equal(t, "STOP_REQUESTED", transition.Kind)
				return nil
			}
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				state, e = f.m.agentRunStateOn(f.human, tx, 7, 202, "STOPPED")
				return e
			}))
			require.Equal(t, "STOPPING", state.ProcessStatus)
			_, e = f.m.agentRunStateOn(f.human, f.db, 7, 202, "ACTIVE")
			require.ErrorIs(t, e, ErrInvalidArgument)
			_, e = f.m.agentRunStateOn(f.human, nil, 7, 202, "invalid")
			require.ErrorIs(t, e, ErrInvalidArgument)
		})
	}
}

func TestAgentProfileValidationAndStorageFailuresPreserveVersion(t *testing.T) {
	for _, kind := range []string{"query", "update", "name-conflict", "read-after-update", "invalid-values"} {
		t.Run(kind, func(t *testing.T) {
			m, db := directoryHarness(t)
			name := "Changed profile"
			patch := AgentProfilePatch{Version: 1, Name: &name}
			injected := errors.New("profile persistence unavailable")
			count := 0
			if kind == "query" || kind == "name-conflict" || kind == "read-after-update" {
				failAt := 1
				if kind == "name-conflict" {
					failAt = 2
				}
				if kind == "read-after-update" {
					failAt = 3
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("profile_storage_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Agents.String() {
						count++
						if count == failAt {
							tx.AddError(injected)
						}
					}
				}))
			} else if kind == "update" {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("profile_storage_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Agents.String() {
						tx.AddError(injected)
					}
				}))
			}
			if kind == "invalid-values" {
				empty := ""
				long := strings.Repeat("x", 2001)
				risk := "UNKNOWN"
				duplicates := []string{"review", "review"}
				many := make([]string, 33)
				badPolicy := json.RawMessage(`[] trailing`)
				largePolicy := json.RawMessage(`"` + strings.Repeat("x", 8192) + `"`)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					for _, in := range []AgentProfilePatch{{Name: &empty}, {Persona: &long}, {MaxRisk: &risk}, {DefaultChannelNames: &duplicates}, {MCPAllowlist: &many}, {MemoryPolicy: &badPolicy}, {MemoryPolicy: &largePolicy}} {
						in.Version = 1
						_, e := m.updateAgentProfileOn(t.Context(), tx, 7, 101, in)
						require.ErrorIs(t, e, ErrInvalidArgument)
					}
					return nil
				}))
			} else {
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.updateAgentProfileOn(t.Context(), tx, 7, 101, patch); return e }), injected)
			}
			require.NoError(t, db.Callback().Query().Remove("profile_storage_failure"))
			require.NoError(t, db.Callback().Update().Remove("profile_storage_failure"))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				profile, e := m.agentProfileOn(t.Context(), tx, 7, 101)
				require.NoError(t, e)
				require.Equal(t, 1, profile.Version)
				require.Equal(t, "Agent", profile.Name)
				return nil
			}))
		})
	}
}

func TestRetirementCommandsRollbackEveryPersistedReadAndWriteFailure(t *testing.T) {
	for _, command := range []string{"begin", "cancel", "cleanup", "force", "complete", "timeout"} {
		t.Run(command, func(t *testing.T) {
			exercise := func(t *testing.T, kind string, failAt int) int {
				t.Helper()
				f := agentRebindHarness(t)
				f.m.revokeAccount = func(_ context.Context, tx *gorm.DB, _, _ guid.ID) error {
					return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('retirement_account_revoked')").Error
				}
				f.m.retirementObserver = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID) error {
					return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('retirement_knowledge_frozen')").Error
				}
				revoke := func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID) error {
					return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('retirement_bindings_revoked')").Error
				}
				var binding AgentRetirementBinding
				if command != "begin" && command != "force" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
						var e error
						binding, e = f.m.beginAgentRetirementOn(f.human, tx, 7, 202, 901, 1)
						return e
					}))
				}
				if command == "complete" || command == "timeout" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
						var e error
						binding, e = f.m.cleanupAgentRetirementOn(f.human, tx, 7, 202, 901, binding.Version)
						return e
					}))
					if command == "complete" {
						require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
							commands, e := f.m.pullAgentMachineCommandsOn(f.oldRunner, tx, 7, 1, f.oldSession.ID, f.oldSession.ConnEpoch)
							if e != nil {
								return e
							}
							require.Len(t, commands, 1)
							_, e = f.m.reportAgentMachineCommandOn(f.oldRunner, tx, 7, 1, commands[0].ID, f.oldSession.ID, f.oldSession.ConnEpoch, "DONE", "")
							return e
						}))
					} else {
						require.NoError(t, f.db.Table(f.m.names.AgentMachineCommands.String()).Where("id=?", *binding.CleanupCommandID).Update("created_at", time.Now().Add(-6*time.Minute)).Error)
					}
				}
				before := f.agent(t)
				var effectsBefore int64
				require.NoError(t, f.db.Table("test_fleet_effects").Count(&effectsBefore).Error)
				commands := query.BindAgentMachineCommands(f.db, f.m.names)
				commandsBefore, e := commands.WithContext(t.Context()).Order(commands.ID).Find()
				require.NoError(t, e)
				roles := query.BindAgentRoles(f.db, f.m.names)
				rolesBefore, e := roles.WithContext(t.Context()).Order(roles.AgentID, roles.Role).Find()
				require.NoError(t, e)
				injected := errors.New("retirement persistence interrupted")
				count := 0
				hook := func(tx *gorm.DB) {
					if strings.HasPrefix(tx.Statement.Table, "dir_fleet_") {
						count++
						if failAt > 0 && count == failAt {
							tx.AddError(injected)
						}
					}
				}
				switch kind {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("retirement_storage_failure", hook))
				case "create":
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("retirement_storage_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("retirement_storage_failure", hook))
				}
				e = f.db.Transaction(func(tx *gorm.DB) error {
					var e error
					switch command {
					case "begin":
						_, e = f.m.beginAgentRetirementOn(f.human, tx, 7, 202, 901, before.Version)
					case "cancel":
						_, e = f.m.cancelAgentRetirementOn(f.human, tx, 7, 202, 901, before.Version)
					case "cleanup":
						_, e = f.m.cleanupAgentRetirementOn(f.human, tx, 7, 202, 901, before.Version)
					case "force":
						_, e = f.m.forceAgentRetirementOn(f.human, tx, 7, 202, 901, before.Version, before.Name, RetirementLoss{}, revoke)
					case "complete":
						_, e = f.m.completeAgentRetirementRevocationsOn(f.human, tx, 7, 202, 901, before.Version, revoke)
					case "timeout":
						_, e = f.m.completeTimedOutRetirementOn(f.human, tx, 7, 202, 901, before.Version, revoke)
					}
					return e
				})
				require.NoError(t, f.db.Callback().Query().Remove("retirement_storage_failure"))
				require.NoError(t, f.db.Callback().Create().Remove("retirement_storage_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("retirement_storage_failure"))
				if failAt == 0 {
					require.NoError(t, e)
					return count
				}
				require.ErrorIs(t, e, injected)
				require.Equal(t, before, f.agent(t), "all Agent fields must roll back")
				var effectsAfter int64
				require.NoError(t, f.db.Table("test_fleet_effects").Count(&effectsAfter).Error)
				require.Equal(t, effectsBefore, effectsAfter)
				commandsAfter, e := commands.WithContext(t.Context()).Order(commands.ID).Find()
				require.NoError(t, e)
				require.Equal(t, commandsBefore, commandsAfter)
				rolesAfter, e := roles.WithContext(t.Context()).Order(roles.AgentID, roles.Role).Find()
				require.NoError(t, e)
				require.Equal(t, rolesBefore, rolesAfter)
				return count
			}
			for _, kind := range []string{"query", "create", "update"} {
				count := exercise(t, kind, 0)
				for point := 1; point <= count; point++ {
					t.Run(fmt.Sprintf("%s/%d", kind, point), func(t *testing.T) { exercise(t, kind, point) })
				}
			}
		})
	}
}

func TestRetirementCompletionRequiresCurrentQuietSourceAndEveryRevocationOwner(t *testing.T) {
	for _, command := range []string{"cleanup", "timeout", "complete"} {
		for _, fault := range []string{"missing-quiet", "quiet-error", "running-executions", "missing-account", "missing-observer", "missing-audit", "missing-events"} {
			if command == "cleanup" && (fault == "missing-account" || fault == "missing-observer" || fault == "missing-audit" || fault == "missing-events") {
				continue
			}
			t.Run(command+"/"+fault, func(t *testing.T) {
				f := agentRebindHarness(t)
				f.m.revokeAccount = func(_ context.Context, tx *gorm.DB, _, _ guid.ID) error {
					return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('account_revoked')").Error
				}
				f.m.retirementObserver = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID) error {
					return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('retirement_observed')").Error
				}
				revoke := func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID) error {
					return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('source_revoked')").Error
				}
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.beginAgentRetirementOn(f.human, tx, 7, 202, 901, 1); return e }))
				if command != "cleanup" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
						_, e := f.m.cleanupAgentRetirementOn(f.human, tx, 7, 202, 901, f.agent(t).Version)
						return e
					}))
					if command == "complete" {
						require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
							commands, e := f.m.pullAgentMachineCommandsOn(f.oldRunner, tx, 7, 1, f.oldSession.ID, f.oldSession.ConnEpoch)
							if e != nil {
								return e
							}
							require.Len(t, commands, 1)
							_, e = f.m.reportAgentMachineCommandOn(f.oldRunner, tx, 7, 1, commands[0].ID, f.oldSession.ID, f.oldSession.ConnEpoch, "DONE", "")
							return e
						}))
					} else {
						require.NoError(t, f.db.Table(f.m.names.AgentMachineCommands.String()).Where("kind=?", "CLEANUP").Update("created_at", time.Now().UTC().Add(-6*time.Minute)).Error)
					}
				}
				before := f.agent(t)
				injected := errors.New("retirement quiet source unavailable")
				want := ErrUnavailable
				switch fault {
				case "missing-quiet":
					f.m.agentRebindQuiet = nil
				case "quiet-error":
					f.m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, injected }
					want = injected
				case "running-executions":
					f.m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil }
					want = ErrAgentRunningExecutions
				case "missing-account":
					f.m.revokeAccount = nil
				case "missing-observer":
					f.m.retirementObserver = nil
				case "missing-audit":
					f.m.audit = nil
				case "missing-events":
					f.m.events = nil
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					switch command {
					case "cleanup":
						_, e := f.m.cleanupAgentRetirementOn(f.human, tx, 7, 202, 901, before.Version)
						return e
					case "timeout":
						_, e := f.m.completeTimedOutRetirementOn(f.human, tx, 7, 202, 901, before.Version, revoke)
						return e
					default:
						_, e := f.m.completeAgentRetirementRevocationsOn(f.human, tx, 7, 202, 901, before.Version, revoke)
						return e
					}
				})
				require.ErrorIs(t, err, want)
				require.Equal(t, before, f.agent(t))
				var effects int64
				require.NoError(t, f.db.Table("test_fleet_effects").Where("kind IN ?", []string{"account_revoked", "retirement_observed", "source_revoked"}).Count(&effects).Error)
				require.Zero(t, effects)
			})
		}
	}
}

func TestFleetRosterPaginationAndLineageFiltersUsePersistedOwnership(t *testing.T) {
	f := agentRebindHarness(t)
	q := query.BindAgents(f.db, f.m.names)
	base := f.agent(t)
	for i, lineage := range []string{"SPAWNED", "ADOPTED", "ORPHANED"} {
		agent := *base
		agent.ID = guid.ID(1000 + i)
		agent.PrincipalID = guid.ID(2000 + i)
		agent.Handle = fmt.Sprintf("worker-%d", i)
		agent.Name = lineage
		parent, grant, reason := base.ID, guid.ID(901), "Bounded spawn provenance"
		agent.Origin = "AGENT_SPAWNED"
		agent.ParentAgentID = &parent
		agent.SpawnGrantID = &grant
		agent.SpawnReason = &reason
		now := time.Now().UTC()
		if lineage == "ADOPTED" {
			agent.AdoptedAt = &now
		}
		if lineage == "ORPHANED" {
			agent.OrphanedAt = &now
		}
		require.NoError(t, q.WithContext(t.Context()).Create(&agent))
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		total, e := q.WithContext(t.Context()).Where(q.TenantID.Eq(guid.ID(7))).Count()
		require.NoError(t, e)
		page, e := f.m.fleetRosterOn(f.human, tx, 7, FleetRosterFilter{}, 2, 0)
		require.NoError(t, e)
		require.Len(t, page.Items, 2)
		require.True(t, page.HasMore)
		next, e := f.m.fleetRosterOn(f.human, tx, 7, FleetRosterFilter{}, 2, int(total)-2)
		require.NoError(t, e)
		require.Len(t, next.Items, 2)
		require.False(t, next.HasMore)
		require.NotEqual(t, page.Items[1].ID, next.Items[0].ID)
		for _, lineage := range []string{"SPAWNED", "ADOPTED", "ORPHANED"} {
			filtered, e := f.m.fleetRosterOn(f.human, tx, 7, FleetRosterFilter{Lineage: lineage, ParentAgentID: base.ID, SpawnGrantID: 901, MachineID: 1}, 10, 0)
			require.NoError(t, e)
			require.Len(t, filtered.Items, 1)
			require.Equal(t, lineage, filtered.Items[0].Lineage)
			require.True(t, filtered.Items[0].MachineOnline)
		}
		_, e = f.m.fleetRosterOn(f.human, tx, 7, FleetRosterFilter{Lineage: "INVALID"}, 10, 0)
		require.ErrorIs(t, e, ErrInvalidArgument)
		_, e = f.m.fleetRosterOn(f.human, tx, 7, FleetRosterFilter{}, 10, -1)
		require.ErrorIs(t, e, ErrInvalidArgument)
		return nil
	}))
	for _, table := range []string{f.m.names.Agents.String(), f.m.names.AgentRoles.String(), f.m.names.Machines.String()} {
		injected := errors.New("roster current source unavailable")
		require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("roster_storage_failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(injected)
			}
		}))
		require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
			_, e := f.m.fleetRosterOn(f.human, tx, 7, FleetRosterFilter{}, 10, 0)
			return e
		}), injected)
		require.NoError(t, f.db.Callback().Query().Remove("roster_storage_failure"))
	}
}

func TestAgentProfileUsesActualCurrentDiscoveryForMCPAndReasoningConfiguration(t *testing.T) {
	f := agentRebindHarness(t)
	mcp := []string{"catalog:reader"}
	effort := "high"
	for _, kind := range []string{"MODELS", "CAPABILITIES"} {
		facts := []RuntimeDiscovery{{Runtime: "CLAUDE_CODE", State: "READY", Source: "LOCAL_CONFIG"}, {Runtime: "CODEX", State: "UNSUPPORTED", Source: "LOCAL_CONFIG", Reason: "NO_LOCAL_MODEL_LIST_OR_CLI_LIST_COMMAND"}}
		if kind == "MODELS" {
			facts[0].Source = "CLI_CACHE"
			facts[0].Models = []string{"sonnet"}
			facts[0].Reasoning = []ModelReasoning{{Model: "sonnet", Efforts: []string{"low", "high"}}}
		} else {
			facts[0].MCP = mcp
		}
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			_, e := f.m.RecordDiscoveryOn(f.oldRunner, tx, 7, 1, f.oldSession.ID, f.oldSession.ConnEpoch, kind, facts)
			return e
		}))
	}
	patch := AgentProfilePatch{Version: 1, ReasoningEffort: &effort, MCPAllowlist: &mcp}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		profile, e := f.m.updateAgentProfileOn(f.human, tx, 7, 202, patch)
		require.NoError(t, e)
		require.Equal(t, mcp, profile.MCPAllowlist)
		require.Equal(t, &effort, profile.ReasoningEffort)
		q := query.BindAgents(tx, f.m.names)
		_, e = q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).Update(q.State, "ACTIVE")
		require.NoError(t, e)
		allowed, e := f.m.agentCatalogAllowedOn(f.human, tx, 7, 202, "reader")
		require.NoError(t, e)
		require.True(t, allowed)
		allowed, e = f.m.agentCatalogAllowedOn(f.human, tx, 7, 202, "other")
		require.NoError(t, e)
		require.False(t, allowed)
		return nil
	}))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.rearmAgentOn(f.human, tx, 7, 202) }))
	require.Equal(t, "STARTING", f.agent(t).ProcessStatus)
	_, e := f.m.agentCatalogAllowedOn(f.human, f.db, 7, 202, "reader")
	require.ErrorIs(t, e, ErrInvalidArgument)
	require.ErrorIs(t, f.m.rearmAgentOn(f.human, f.db, 7, 202), ErrInvalidArgument)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.agentCatalogAllowedOn(f.human, tx, 7, 999, "reader")
		require.ErrorIs(t, e, gorm.ErrRecordNotFound)
		return nil
	}))
}

func TestAgentProfileCurrentDiscoveryStorageFailureDoesNotAdvanceConfiguration(t *testing.T) {
	for _, target := range []string{"machine", "inspection"} {
		t.Run(target, func(t *testing.T) {
			f := agentRebindHarness(t)
			before := f.agent(t)
			effort := "high"
			injected := errors.New("current discovery unavailable")
			table := f.m.names.Inspections.String()
			if target == "machine" {
				table = f.m.names.Machines.String()
			}
			require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("profile_discovery_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(injected)
				}
			}))
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, e := f.m.updateAgentProfileOn(f.human, tx, 7, 202, AgentProfilePatch{Version: before.Version, ReasoningEffort: &effort})
				return e
			}), injected)
			require.NoError(t, f.db.Callback().Query().Remove("profile_discovery_failure"))
			require.Equal(t, before, f.agent(t))
		})
	}
}

func TestAgentProvisioningEveryStorageFailureRollsBackProfileRolesAndPrivateDelivery(t *testing.T) {
	for _, kind := range []string{"query", "create", "update"} {
		total := 0
		run := func(t *testing.T, at int) {
			m, db, ctx, _, _, _, in := agentRegistrationHarness(t)
			m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
			tables := []string{m.names.Agents.String(), m.names.AgentRoles.String(), m.names.AgentMachineCommands.String()}
			before := map[string]int64{}
			for _, table := range tables {
				var n int64
				require.NoError(t, db.Table(table).Count(&n).Error)
				before[table] = n
			}
			count := 0
			injected := errors.New("atomic provisioning storage failure")
			hook := func(tx *gorm.DB) {
				if !strings.Contains(tx.Statement.Table, "fleet_") {
					return
				}
				count++
				if count == at {
					tx.AddError(injected)
				}
			}
			switch kind {
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("provisioning_failure", hook))
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("provisioning_failure", hook))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("provisioning_failure", hook))
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.provisionAgentOn(ctx, tx, 7, in, AgentProfilePatch{}, identity.ServiceAccountCredentialSecret{Credential: identity.ServiceAccountCredential{ID: 303, ServiceAccountID: in.PrincipalID}, Secret: "never-persist-as-plaintext"})
				return e
			})
			require.NoError(t, db.Callback().Query().Remove("provisioning_failure"))
			require.NoError(t, db.Callback().Create().Remove("provisioning_failure"))
			require.NoError(t, db.Callback().Update().Remove("provisioning_failure"))
			if at == 0 {
				require.NoError(t, err)
				total = count
				return
			}
			require.ErrorIs(t, err, injected)
			for _, table := range tables {
				var n int64
				require.NoError(t, db.Table(table).Count(&n).Error)
				require.Equal(t, before[table], n)
			}
		}
		t.Run(kind+"/success", func(t *testing.T) { run(t, 0) })
		for at := 1; at <= total; at++ {
			t.Run(fmt.Sprintf("%s/%d", kind, at), func(t *testing.T) { run(t, at) })
		}
	}
}

func TestAgentWorkspaceCurrentSourceAndRetirementIdentifierFailuresPreserveBinding(t *testing.T) {
	for _, fault := range []string{"agent_read", "machine_read", "missing_machine", "offline", "missing_agent", "no_transaction", "cleanup_id_error", "cleanup_id_zero", "force_id_error", "force_id_zero"} {
		t.Run(fault, func(t *testing.T) {
			f := agentRebindHarness(t)
			before := f.agent(t)
			injected := errors.New("workspace source failure")
			if strings.HasPrefix(fault, "cleanup") {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.beginAgentRetirementOn(f.human, tx, 7, 202, 900, 1); return e }))
				before = f.agent(t)
			}
			var baselineEffects int64
			require.NoError(t, f.db.Table("test_fleet_effects").Count(&baselineEffects).Error)
			switch fault {
			case "agent_read", "machine_read":
				table := f.m.names.Agents.String()
				if fault == "machine_read" {
					table = f.m.names.Machines.String()
				}
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("workspace_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(injected)
					}
				}))
			case "missing_machine":
				require.NoError(t, f.db.Table(f.m.names.Agents.String()).Where("principal_id=?", 202).Update("machine_id", nil).Error)
				before = f.agent(t)
			case "offline":
				require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 1).Update("state", "OFFLINE").Error)
			case "cleanup_id_error", "force_id_error":
				f.m.ids = failedFleetIDs{err: injected}
			case "cleanup_id_zero", "force_id_zero":
				f.m.ids = failedFleetIDs{}
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				if strings.HasPrefix(fault, "cleanup") {
					_, e := f.m.cleanupAgentRetirementOn(f.human, tx, 7, 202, 900, before.Version)
					return e
				}
				if strings.HasPrefix(fault, "force") {
					_, e := f.m.forceAgentRetirementOn(f.human, tx, 7, 202, 900, before.Version, before.Name, RetirementLoss{}, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil })
					return e
				}
				id := guid.ID(202)
				if fault == "missing_agent" {
					id = 999
				}
				if fault == "no_transaction" {
					tx = f.db
				}
				fence, e := f.m.agentWorkspaceBindingOn(f.human, tx, 7, id)
				require.Zero(t, fence)
				return e
			})
			require.Error(t, err)
			require.NoError(t, f.db.Callback().Query().Remove("workspace_failure"))
			require.Equal(t, before, f.agent(t))
			var effects int64
			require.NoError(t, f.db.Table("test_fleet_effects").Count(&effects).Error)
			require.Equal(t, baselineEffects, effects)
		})
	}
}

func TestWorkspaceCapabilitiesKeepSingleProcessAndRetirementObserverOwner(t *testing.T) {
	f := agentRebindHarness(t)
	f.m.revokeAccount = func(_ context.Context, tx *gorm.DB, _, _ guid.ID) error {
		return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('account_disabled')").Error
	}
	f.m.processObserver = nil
	f.m.retirementObserver = nil
	services := module.NewServices()
	require.NoError(t, f.m.provideWorkspace(services))
	require.NoError(t, f.m.provideRetirementObservers(services))
	require.NoError(t, services.Seal())
	profiles, e := module.Resolve[AgentProfiles](services)
	require.NoError(t, e)
	observer, e := module.Resolve[AgentRetirementObserverConnector](services)
	require.NoError(t, e)
	require.ErrorIs(t, profiles.BindProcessObserver(nil), ErrUnavailable)
	require.ErrorIs(t, observer.Bind(nil), ErrUnavailable)
	require.NoError(t, profiles.BindProcessObserver(func(_ context.Context, tx *gorm.DB, event AgentProcessTransition) error {
		require.Equal(t, "START_REQUESTED", event.Kind)
		return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('process_observed')").Error
	}))
	require.ErrorIs(t, profiles.BindProcessObserver(func(context.Context, *gorm.DB, AgentProcessTransition) error {
		t.Fatal("replacement observer invoked")
		return nil
	}), ErrUnavailable)
	require.NoError(t, observer.Bind(func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID) error {
		return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('retirement_observed')").Error
	}))
	require.ErrorIs(t, observer.Bind(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error {
		t.Fatal("replacement observer invoked")
		return nil
	}), ErrUnavailable)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := profiles.RunStateOn(f.human, tx, 7, 202, "ACTIVE"); return e }))
	revokers, e := module.Resolve[AgentRetirementRevocationConnector](services)
	require.NoError(t, e)
	runtime, e := module.Resolve[AgentRetirementRuntime](services)
	require.NoError(t, e)
	revoke := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	require.ErrorIs(t, revokers.Bind("", revoke), ErrInvalidArgument)
	require.ErrorIs(t, revokers.Bind("collab", nil), ErrInvalidArgument)
	for _, owner := range []string{"collab", "business_governance"} {
		require.NoError(t, revokers.Bind(owner, revoke))
		require.ErrorIs(t, revokers.Bind(owner, revoke), ErrInvalidArgument)
	}
	before := f.agent(t)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		out, e := runtime.ForceOn(f.human, tx, 7, 202, 900, before.Version, before.Name, RetirementLoss{})
		require.NoError(t, e)
		require.Equal(t, "RETIRED", out.State)
		return e
	}))
	for _, kind := range []string{"process_observed", "retirement_observed"} {
		var n int64
		require.NoError(t, f.db.Table("test_fleet_effects").Where("kind=?", kind).Count(&n).Error)
		require.EqualValues(t, 1, n)
	}
}

func TestAgentProfileInvalidVersionCurrentBindingAndConfigurationShapePreserveRevision(t *testing.T) {
	for _, fault := range []string{"version", "retiring", "unbound", "no_session", "capability_tag", "mcp_shape", "tags_shape", "channels_shape", "missing_agent", "no_transaction"} {
		t.Run(fault, func(t *testing.T) {
			f := agentRebindHarness(t)
			effort := "high"
			patch := AgentProfilePatch{Version: 1, ReasoningEffort: &effort}
			switch fault {
			case "version":
				patch.Version = 0
			case "retiring":
				require.NoError(t, f.db.Table(f.m.names.Agents.String()).Where("principal_id=?", 202).Update("state", "RETIRING").Error)
			case "unbound":
				require.NoError(t, f.db.Table(f.m.names.Agents.String()).Where("principal_id=?", 202).Update("machine_id", nil).Error)
			case "no_session":
				require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 1).Updates(map[string]any{"state": "OFFLINE", "current_session_id": nil}).Error)
			case "capability_tag":
				tags := []string{"UPPER CASE"}
				patch = AgentProfilePatch{Version: 1, CapabilityTags: &tags}
			case "mcp_shape", "tags_shape", "channels_shape":
				column := map[string]string{"mcp_shape": "mcp_allowlist", "tags_shape": "capability_tags", "channels_shape": "default_channel_names"}[fault]
				require.NoError(t, f.db.Table(f.m.names.Agents.String()).Where("principal_id=?", 202).Update(column, `{}`).Error)
			}
			before := f.agent(t)
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				if strings.HasSuffix(fault, "shape") {
					_, e := f.m.agentProfileOn(f.human, tx, 7, 202)
					return e
				}
				principal := guid.ID(202)
				if fault == "missing_agent" {
					principal = 999
				}
				if fault == "no_transaction" {
					tx = f.db
				}
				_, e := f.m.updateAgentProfileOn(f.human, tx, 7, principal, patch)
				return e
			}))
			require.Equal(t, before, f.agent(t))
		})
	}
}
