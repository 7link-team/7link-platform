package fleet

import (
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAgentMachineCommandsStorageFailuresPreserveBindingAndPrivateDelivery(t *testing.T) {
	for _, command := range []string{"pull", "report", "expire"} {
		t.Run(command, func(t *testing.T) {
			exercise := func(t *testing.T, operation string, failAt int) int {
				f := agentRebindHarness(t)
				_, err := f.rebind()
				require.NoError(t, err)
				q := query.BindAgentMachineCommands(f.db, f.m.names)
				if command == "report" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
						_, e := f.m.pullAgentMachineCommandsOn(f.newRunner, tx, 7, 2, f.newSession.ID, f.newSession.ConnEpoch)
						return e
					}))
				}
				before := f.agent(t)
				commands, err := q.WithContext(t.Context()).Order(q.ID).Find()
				require.NoError(t, err)
				var id guid.ID
				for _, row := range commands {
					if row.Kind == "CREDENTIAL" {
						id = row.ID
					}
				}
				injected := errors.New("command storage unavailable")
				count := 0
				hook := func(tx *gorm.DB) {
					if tx.Statement.Table != f.m.names.AgentMachineCommands.String() && tx.Statement.Table != f.m.names.Agents.String() {
						return
					}
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch operation {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("command_storage_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("command_storage_failure", hook))
				}
				if command == "expire" {
					err = f.m.expireAgentMachineCommands(t.Context(), f.db, 7, time.Now().UTC().Add(31*24*time.Hour))
				} else {
					err = f.db.Transaction(func(tx *gorm.DB) error {
						if command == "pull" {
							_, e := f.m.pullAgentMachineCommandsOn(f.newRunner, tx, 7, 2, f.newSession.ID, f.newSession.ConnEpoch)
							return e
						}
						_, e := f.m.reportAgentMachineCommandOn(f.newRunner, tx, 7, 2, id, f.newSession.ID, f.newSession.ConnEpoch, "DONE", "")
						return e
					})
				}
				require.NoError(t, f.db.Callback().Query().Remove("command_storage_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("command_storage_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				if command != "expire" {
					require.Equal(t, before, f.agent(t))
					after, e := q.WithContext(t.Context()).Order(q.ID).Find()
					require.NoError(t, e)
					require.Equal(t, commands, after)
				} else {
					// Expiry commits each command independently. The credential command
					// must retain its sealed payload unless its own transaction committed.
					after, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
					require.NoError(t, e)
					if after.State == "FAILED" {
						require.Empty(t, after.SealedCredential)
						require.Equal(t, "FAILED", f.agent(t).CredentialDelivery)
					} else {
						require.Equal(t, "QUEUED", after.State)
						require.NotEmpty(t, after.SealedCredential)
						require.Equal(t, "PENDING", f.agent(t).CredentialDelivery)
					}
				}
				return count
			}
			for _, operation := range []string{"query", "update"} {
				count := exercise(t, operation, 0)
				for point := 1; point <= count; point++ {
					t.Run(fmt.Sprintf("%s/%d", operation, point), func(t *testing.T) { exercise(t, operation, point) })
				}
			}
		})
	}
}

func TestAgentCommandActualReceiptsFenceDeliveryAndCleanup(t *testing.T) {
	f := agentRebindHarness(t)
	_, err := f.rebind()
	require.NoError(t, err)
	pull := func(ctx context.Context, machine guid.ID, session SessionFact, epoch int64) ([]AgentMachineDelivery, error) {
		var out []AgentMachineDelivery
		e := f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = f.m.pullAgentMachineCommandsOn(ctx, tx, 7, machine, session.ID, epoch)
			return e
		})
		return out, e
	}
	report := func(ctx context.Context, machine guid.ID, session SessionFact, id guid.ID, state, reason string) (AgentMachineCommandView, error) {
		var out AgentMachineCommandView
		e := f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = f.m.reportAgentMachineCommandOn(ctx, tx, 7, machine, id, session.ID, session.ConnEpoch, state, reason)
			return e
		})
		return out, e
	}
	_, err = pull(f.newRunner, 2, f.newSession, f.newSession.ConnEpoch+1)
	require.ErrorIs(t, err, ErrSessionStale)
	commands, err := pull(f.newRunner, 2, f.newSession, f.newSession.ConnEpoch)
	require.NoError(t, err)
	require.Len(t, commands, 1)
	require.Equal(t, "private-agent-secret", commands[0].Secret)
	require.Equal(t, "PENDING", f.agent(t).CredentialDelivery)
	delivered, err := report(f.newRunner, 2, f.newSession, commands[0].ID, "DONE", "")
	require.NoError(t, err)
	require.Equal(t, "DONE", delivered.State)
	require.Equal(t, "DELIVERED", f.agent(t).CredentialDelivery)
	require.NotNil(t, f.agent(t).PendingCleanupMachineID)
	replay, err := report(f.newRunner, 2, f.newSession, commands[0].ID, "DONE", "")
	require.NoError(t, err)
	require.Equal(t, delivered, replay)
	cleanup, err := pull(f.oldRunner, 1, f.oldSession, f.oldSession.ConnEpoch)
	require.NoError(t, err)
	require.Len(t, cleanup, 1)
	require.Equal(t, "CLEANUP", cleanup[0].Kind)
	require.Empty(t, cleanup[0].Secret)
	_, err = report(f.oldRunner, 1, f.oldSession, cleanup[0].ID, "DONE", "")
	require.NoError(t, err)
	require.Nil(t, f.agent(t).PendingCleanupMachineID)
}

func TestFailedCleanupRetainsPendingCleanupAndNeverClaimsSuccess(t *testing.T) {
	f := agentRebindHarness(t)
	_, err := f.rebind()
	require.NoError(t, err)
	var commands []AgentMachineDelivery
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		commands, e = f.m.pullAgentMachineCommandsOn(f.oldRunner, tx, 7, 1, f.oldSession.ID, f.oldSession.ConnEpoch)
		return e
	}))
	require.Len(t, commands, 1)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		out, e := f.m.reportAgentMachineCommandOn(f.oldRunner, tx, 7, 1, commands[0].ID, f.oldSession.ID, f.oldSession.ConnEpoch, "FAILED", "CLEANUP_FAILED")
		require.Equal(t, "FAILED", out.State)
		return e
	}))
	require.NotNil(t, f.agent(t).PendingCleanupMachineID)
	require.Equal(t, "PENDING", f.agent(t).CredentialDelivery)
}

func TestReboundWorkerAndRuntimeRequireDeliveryButAllowDeferredOldCleanup(t *testing.T) {
	f := agentRebindHarness(t)
	_, err := f.rebind()
	require.NoError(t, err)
	q := query.BindAgents(f.db, f.m.names)
	_, err = q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).Update(q.State, "ACTIVE")
	require.NoError(t, err)
	for _, phase := range []string{"credential-pending", "cleanup-pending", "ready"} {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			profiles, e := f.m.AgentWorkersOn(f.newRunner, tx, 7, 2, f.newSession.ID, f.newSession.ConnEpoch)
			require.NoError(t, e)
			_, found, runtimeErr := f.m.agentRuntimeOn(f.human, tx, 7, 202)
			if phase != "credential-pending" {
				require.Len(t, profiles, 1)
				require.NoError(t, runtimeErr)
				require.True(t, found)
			} else {
				require.Empty(t, profiles)
				require.ErrorIs(t, runtimeErr, ErrVersionConflict)
				require.False(t, found)
			}
			return nil
		}))
		if phase == "ready" {
			break
		}
		ctx, machine, session := f.newRunner, guid.ID(2), f.newSession
		if phase == "cleanup-pending" {
			ctx, machine, session = f.oldRunner, 1, f.oldSession
		}
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			commands, e := f.m.pullAgentMachineCommandsOn(ctx, tx, 7, machine, session.ID, session.ConnEpoch)
			require.NoError(t, e)
			require.Len(t, commands, 1)
			_, e = f.m.reportAgentMachineCommandOn(ctx, tx, 7, machine, commands[0].ID, session.ID, session.ConnEpoch, "DONE", "")
			return e
		}))
	}
}

func TestAgentCommandExpiryErasesSecretWithoutClaimingCleanupAndRollsBackOnOutboxFailure(t *testing.T) {
	f := agentRebindHarness(t)
	_, err := f.rebind()
	require.NoError(t, err)
	q := query.BindAgentMachineCommands(f.db, f.m.names)
	commands, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, commands, 2)
	future := time.Now().UTC().Add(31 * 24 * time.Hour)
	f.publisher.fail = true
	require.Error(t, f.m.expireAgentMachineCommands(t.Context(), f.db, 7, future))
	current, err := q.WithContext(t.Context()).Where(q.ID.Eq(commands[0].ID)).First()
	require.NoError(t, err)
	require.Equal(t, "QUEUED", current.State)
	f.publisher.fail = false
	require.NoError(t, f.m.expireAgentMachineCommands(t.Context(), f.db, 7, future))
	commands, err = q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	for _, cmd := range commands {
		require.Equal(t, "FAILED", cmd.State)
		require.Empty(t, cmd.SealedCredential)
		require.Equal(t, "COMMAND_EXPIRED", cmd.ReasonCode)
	}
	require.Equal(t, "FAILED", f.agent(t).CredentialDelivery)
	require.NotNil(t, f.agent(t).PendingCleanupMachineID)
}

func TestPrivateAgentDeliveryRejectsCurrentLockKeyAndCredentialCorruption(t *testing.T) {
	for _, fault := range []string{"missing_lock", "lock", "actor", "stale_session", "missing_key", "wrong_key", "ciphertext", "payload", "no_transaction"} {
		t.Run(fault, func(t *testing.T) {
			f := agentRebindHarness(t)
			_, err := f.rebind()
			require.NoError(t, err)
			q := query.BindAgentMachineCommands(f.db, f.m.names)
			commands, e := q.WithContext(t.Context()).Order(q.ID).Find()
			require.NoError(t, e)
			var credentialID guid.ID
			for _, command := range commands {
				if command.Kind == "CREDENTIAL" {
					credentialID = command.ID
				}
			}
			require.Positive(t, credentialID)
			ctx := f.newRunner
			injected := errors.New("private credential current owner failure")
			switch fault {
			case "missing_lock":
				f.m.principals.Lock = nil
			case "lock":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "actor":
				ctx = t.Context()
			case "missing_key":
				f.m.config.GatewayEncryptionKey = ""
			case "wrong_key":
				key := make([]byte, 32)
				key[0] = 1
				f.m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(key)
			case "ciphertext":
				require.NoError(t, f.db.Table(f.m.names.AgentMachineCommands.String()).Where("id=?", credentialID).Update("sealed_credential", "broken ciphertext").Error)
			case "payload":
				command := commands[len(commands)-1]
				for _, item := range commands {
					if item.ID == credentialID {
						command = item
					}
				}
				ring, e := f.m.agentCredentialRing()
				require.NoError(t, e)
				sealed, e := ring.Seal("binding", []byte("7:"+command.AgentPrincipalID.String()+":"+command.CredentialID.String()), []byte(`{"client_secret":"wrong shape"}`))
				require.NoError(t, e)
				require.NoError(t, f.db.Table(f.m.names.AgentMachineCommands.String()).Where("id=?", credentialID).Update("sealed_credential", sealed).Error)
			}
			before := f.agent(t)
			epoch := f.newSession.ConnEpoch
			if fault == "stale_session" {
				epoch++
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				if fault == "no_transaction" {
					tx = f.db
				}
				out, e := f.m.pullAgentMachineCommandsOn(ctx, tx, 7, 2, f.newSession.ID, epoch)
				require.Empty(t, out)
				return e
			}))
			require.Equal(t, before, f.agent(t))
			saved, e := q.WithContext(t.Context()).Where(q.ID.Eq(credentialID)).First()
			require.NoError(t, e)
			require.Equal(t, "QUEUED", saved.State)
			require.Nil(t, saved.SessionID)
		})
	}
}
