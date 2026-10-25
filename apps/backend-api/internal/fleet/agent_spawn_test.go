package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnAndIdleChildRetirementStorageFailuresRollbackSourceOwnedEffects(t *testing.T) {
	for _, command := range []string{"spawn", "retire"} {
		for _, operation := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				m, db, ctx, _, in := agentSpawnHarness(t)
				var child RegisteredAgent
				if command == "retire" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; child, e = m.SpawnAgentOn(ctx, tx, 7, in); return e }))
				}
				q := query.BindAgents(db, m.names)
				roles := query.BindAgentRoles(db, m.names)
				commands := query.BindAgentMachineCommands(db, m.names)
				before, e := q.WithContext(ctx).Order(q.ID).Find()
				require.NoError(t, e)
				beforeRoles, e := roles.WithContext(ctx).Order(roles.AgentID, roles.Role).Find()
				require.NoError(t, e)
				beforeCommands, e := commands.WithContext(ctx).Order(commands.ID).Find()
				require.NoError(t, e)
				var beforeEffects int64
				require.NoError(t, db.Table("test_fleet_effects").Count(&beforeEffects).Error)
				var beforeUsage []struct {
					Child int64
					State string
				}
				require.NoError(t, db.Table("spawn_usage_probe").Order("child").Find(&beforeUsage).Error)
				count := 0
				injected := errors.New("spawn storage unavailable")
				hook := func(tx *gorm.DB) {
					if !strings.HasPrefix(tx.Statement.Table, "dir_fleet_") {
						return
					}
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch operation {
				case "query":
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("spawn_storage_failure", hook))
				case "create":
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("spawn_storage_failure", hook))
				case "update":
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("spawn_storage_failure", hook))
				}
				err := db.Transaction(func(tx *gorm.DB) error {
					if command == "spawn" {
						_, e := m.SpawnAgentOn(ctx, tx, 7, in)
						return e
					}
					_, e := m.RetireSpawnedAgentOn(ctx, tx, 7, child.ID, child.Version, "Idle child has completed its assignment")
					return e
				})
				require.NoError(t, db.Callback().Query().Remove("spawn_storage_failure"))
				require.NoError(t, db.Callback().Create().Remove("spawn_storage_failure"))
				require.NoError(t, db.Callback().Update().Remove("spawn_storage_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				after, e := q.WithContext(ctx).Order(q.ID).Find()
				require.NoError(t, e)
				require.Equal(t, before, after)
				afterRoles, e := roles.WithContext(ctx).Order(roles.AgentID, roles.Role).Find()
				require.NoError(t, e)
				require.Equal(t, beforeRoles, afterRoles)
				afterCommands, e := commands.WithContext(ctx).Order(commands.ID).Find()
				require.NoError(t, e)
				require.Equal(t, beforeCommands, afterCommands)
				var effects int64
				require.NoError(t, db.Table("test_fleet_effects").Count(&effects).Error)
				require.Equal(t, beforeEffects, effects)
				var usage []struct {
					Child int64
					State string
				}
				require.NoError(t, db.Table("spawn_usage_probe").Order("child").Find(&usage).Error)
				require.Equal(t, beforeUsage, usage)
				return count
			}
			count := exercise(t, 0)
			for point := 1; point <= count; point++ {
				t.Run(fmt.Sprintf("%s/%s/%d", command, operation, point), func(t *testing.T) { exercise(t, point) })
			}
		}
	}
}

type spawnTestPublisher struct {
	events.Publisher
	Last events.Event
}

func (p *spawnTestPublisher) Publish(ctx context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	p.Last = e
	return p.Publisher.Publish(ctx, tx, e)
}

func agentSpawnHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *fleetPublisher, AgentSpawnInput) {
	t.Helper()
	m, db, ctx, _, _, publisher, registration := agentRegistrationHarness(t)
	m.accounts = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID, _ time.Time) (bool, error) {
		return tenant == 7 && (principal == 11 || principal == 101 || principal == 202), nil
	}
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table(m.names.Agents.String()).Create(map[string]any{"id": 900, "tenant_id": 7, "principal_id": 11, "name": "Supervisor", "handle": "supervisor", "avatar_seed": 2, "system_prompt": "Coordinate bounded work", "runtime_kind": "CODEX", "model_id": "gpt-test", "model_family": "openai", "state": "ACTIVE", "process_status": "RUNNING", "created_by_principal_id": 12, "owner_principal_id": 12, "created_at": now, "updated_at": now}).Error)
	require.NoError(t, db.Exec("CREATE TABLE spawn_usage_probe(child INTEGER PRIMARY KEY, state TEXT NOT NULL)").Error)
	m.spawnPolicy = AgentSpawnPolicy{
		FenceOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil },
		AuthorizeOn: func(ctx context.Context, tx *gorm.DB, tenant guid.ID, in AgentSpawnInput, _ bool) (AgentSpawnGrant, error) {
			require.EqualValues(t, 11, authn.PrincipalIDFromContext(ctx))
			return AgentSpawnGrant{GrantID: 901, OwnerPrincipalID: 12, ExpiresAt: now.Add(24 * time.Hour)}, nil
		},
		ReserveOn: func(ctx context.Context, tx *gorm.DB, tenant, child guid.ID, _ AgentSpawnInput) error {
			return tx.Exec("INSERT INTO spawn_usage_probe VALUES(?,?)", child, "ACTIVE").Error
		},
		RetireOn: func(ctx context.Context, tx *gorm.DB, tenant, parent, child, grant guid.ID, release bool) error {
			require.EqualValues(t, 900, parent)
			require.EqualValues(t, 901, grant)
			if release {
				return tx.Exec("UPDATE spawn_usage_probe SET state='RELEASED' WHERE child=?", child).Error
			}
			return nil
		},
	}
	m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }
	m.revokeAccount = func(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) error {
		return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES (?)", "account_disabled").Error
	}
	return m, db, ctx, publisher, AgentSpawnInput{SupervisorAgentID: 900, ProjectIDs: []guid.ID{99}, Effort: "medium", EstimatedCapMicros: 1000000, Reason: "Create bounded delivery capacity", Registration: registration}
}

func TestAgentSpawnKeepsActualAgentProvenanceAndGrantUsage(t *testing.T) {
	m, db, ctx, _, in := agentSpawnHarness(t)
	p := &spawnTestPublisher{Publisher: m.events}
	m.events = p
	originalAudit := m.audit
	var recorded audit.DomainEvent
	m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		recorded = event
		return originalAudit(ctx, tx, event)
	}
	var out RegisteredAgent
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; out, err = m.SpawnAgentOn(ctx, tx, 7, in); return err }))
	q := query.BindAgents(db, m.names)
	a, err := q.WithContext(ctx).Where(q.ID.Eq(out.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "AGENT_SPAWNED", a.Origin)
	require.Equal(t, "ON_REQUEST", a.AuthorizationMode)
	require.EqualValues(t, 900, *a.ParentAgentID)
	require.EqualValues(t, 901, *a.SpawnGrantID)
	require.EqualValues(t, 11, a.CreatedByPrincipalID)
	require.EqualValues(t, 12, a.OwnerPrincipalID)
	require.NotNil(t, a.ExpiresAt)
	require.Equal(t, in.Reason, *a.SpawnReason)
	require.Equal(t, "agent.spawned", p.Last.Type)
	require.Equal(t, "AGENT", p.Last.Actor.Kind)
	require.EqualValues(t, 11, p.Last.Actor.ID)
	require.Equal(t, "POLICY:spawn_grant", p.Last.Actor.AuthoritySource)
	require.Equal(t, "fleet.agent_spawned", recorded.EventType)
	require.Equal(t, "901", recorded.Detail["spawn_grant_id"])
	require.Equal(t, "900", recorded.Detail["supervisor_agent_id"])
	require.Equal(t, in.Reason, recorded.Detail["reason"])
	var state string
	require.NoError(t, db.Raw("SELECT state FROM spawn_usage_probe WHERE child=?", out.ID).Scan(&state).Error)
	require.Equal(t, "ACTIVE", state)
}

func TestAgentSpawnFailuresDoNotLeaveIdentityOrReservation(t *testing.T) {
	for _, fault := range []string{"grant", "quota", "account", "human", "role", "supervisor", "origin", "authority_mode", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, p, in := agentSpawnHarness(t)
			switch fault {
			case "grant":
				m.spawnPolicy.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, AgentSpawnInput, bool) (AgentSpawnGrant, error) {
					return AgentSpawnGrant{}, ErrMachineForbidden
				}
			case "quota":
				m.spawnPolicy.ReserveOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, AgentSpawnInput) error { return ErrVersionConflict }
			case "account":
				m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, nil }
			case "human":
				ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 7, PrincipalID: 12})
			case "role":
				in.Registration.Roles = []string{"ACCEPTOR"}
			case "supervisor":
				require.NoError(t, db.Table(m.names.Agents.String()).Where("id=?", 900).Update("process_status", "NONE").Error)
			case "origin":
				require.NoError(t, db.Table(m.names.Agents.String()).Where("id=?", 900).Updates(map[string]any{"origin": "AGENT_SPAWNED", "parent_agent_id": 1, "spawn_grant_id": 5, "spawn_reason": "An unadopted spawned supervisor"}).Error)
			case "authority_mode":
				in.Registration.AuthorizationMode = "WORKSPACE_WRITE"
			case "outbox":
				p.fail = true
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SpawnAgentOn(ctx, tx, 7, in); return err }))
			var count int64
			require.NoError(t, db.Table(m.names.Agents.String()).Where("principal_id=?", 202).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Table("spawn_usage_probe").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestAgentSpawnLocksRealPrincipalsBeforePolicyAndRejectsLockFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordered", true: "inactive_child"}[fail], func(t *testing.T) {
			m, db, ctx, _, in := agentSpawnHarness(t)
			var locked []guid.ID
			m.principals.Lock = func(_ context.Context, tx *gorm.DB, id guid.ID) error {
				_, transaction := tx.Statement.ConnPool.(gorm.TxCommitter)
				require.True(t, transaction)
				locked = append(locked, id)
				if fail && id == in.Registration.PrincipalID {
					return ErrMachineForbidden
				}
				return nil
			}
			fenced := false
			m.spawnPolicy.FenceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error {
				fenced = true
				require.Equal(t, []guid.ID{11, 101, 202}, locked)
				return nil
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, err := m.SpawnAgentOn(ctx, tx, 7, in); return err })
			if fail {
				require.ErrorIs(t, err, ErrMachineForbidden)
				require.False(t, fenced)
				var count int64
				require.NoError(t, db.Table(m.names.Agents.String()).Where("principal_id=?", 202).Count(&count).Error)
				require.Zero(t, count)
			} else {
				require.NoError(t, err)
				require.True(t, fenced)
			}
		})
	}
}

func TestSpawnedAgentRetirementRejectsCurrentSourceAndAdoptionChanges(t *testing.T) {
	for _, change := range []string{"inactive_supervisor", "other_parent", "adopted_child", "stopped_supervisor", "orphan_supervisor"} {
		t.Run(change, func(t *testing.T) {
			m, db, ctx, _, in := agentSpawnHarness(t)
			var child RegisteredAgent
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; child, err = m.SpawnAgentOn(ctx, tx, 7, in); return err }))
			switch change {
			case "inactive_supervisor":
				m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, nil }
			case "other_parent":
				ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 7, PrincipalID: 101})
			case "adopted_child":
				require.NoError(t, db.Table(m.names.Agents.String()).Where("id=?", child.ID).Update("adopted_at", time.Now().UTC()).Error)
			case "stopped_supervisor":
				require.NoError(t, db.Table(m.names.Agents.String()).Where("id=?", 900).Update("process_status", "NONE").Error)
			case "orphan_supervisor":
				require.NoError(t, db.Table(m.names.Agents.String()).Where("id=?", 900).Update("orphaned_at", time.Now().UTC()).Error)
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.RetireSpawnedAgentOn(ctx, tx, 7, child.ID, 1, "Retirement needs current supervision")
				return err
			}), ErrMachineForbidden)
			var state string
			require.NoError(t, db.Raw("SELECT state FROM spawn_usage_probe WHERE child=?", child.ID).Scan(&state).Error)
			require.Equal(t, "ACTIVE", state)
			var effects int64
			require.NoError(t, db.Table("test_fleet_effects").Where("kind=?", "account_disabled").Count(&effects).Error)
			require.Zero(t, effects)
		})
	}
}

func TestAgentSpawnRetirementRequiresQuietActualChildAndRollsBack(t *testing.T) {
	m, db, ctx, p, in := agentSpawnHarness(t)
	var child RegisteredAgent
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; child, err = m.SpawnAgentOn(ctx, tx, 7, in); return err }))
	call := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.RetireSpawnedAgentOn(ctx, tx, 7, child.ID, 1, "Retire completed capacity safely")
			return err
		})
	}
	m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil }
	require.ErrorIs(t, call(), ErrAgentRunningExecutions)
	m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }
	require.NoError(t, db.Table(m.names.Agents.String()).Where("id=?", child.ID).Update("process_status", "RUNNING").Error)
	require.ErrorIs(t, call(), ErrAgentRebindState)
	require.NoError(t, db.Table(m.names.Agents.String()).Where("id=?", child.ID).Update("process_status", "NONE").Error)
	p.fail = true
	require.Error(t, call())
	var state string
	require.NoError(t, db.Raw("SELECT state FROM spawn_usage_probe WHERE child=?", child.ID).Scan(&state).Error)
	require.Equal(t, "ACTIVE", state)
	p.fail = false
	require.NoError(t, call())
	q := query.BindAgents(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(child.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "RETIRED", row.State)
	require.Nil(t, row.MachineID)
	require.NotNil(t, row.PendingCleanupMachineID)
	require.NotNil(t, row.RetiredAt)
	require.NoError(t, db.Raw("SELECT state FROM spawn_usage_probe WHERE child=?", child.ID).Scan(&state).Error)
	require.Equal(t, "RELEASED", state)
	var count int64
	require.NoError(t, db.Table(m.names.AgentMachineCommands.String()).Where("agent_principal_id=? AND kind='CLEANUP'", 202).Count(&count).Error)
	require.EqualValues(t, 1, count)
	_, err = m.RetireSpawnedAgentOn(ctx, db, 7, child.ID, 2, "Outside a transaction fails")
	require.Error(t, err)
	// Current policy is checked before a receipt or retired state can authorize access.
	m.spawnPolicy.RetireOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, bool) error {
		return errors.New("grant revoked")
	}
	require.ErrorContains(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.agentSpawnRetirementAuthorityOn(ctx, tx, 7, child.ID); return err }), "grant revoked")
}

func TestAgentRetirementObserverCleanupSharesOriginalRetirementTransaction(t *testing.T) {
	m, db, ctx, _, in := agentSpawnHarness(t)
	var child RegisteredAgent
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; child, err = m.SpawnAgentOn(ctx, tx, 7, in); return err }))
	m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }
	require.NoError(t, db.Exec("CREATE TABLE retirement_cleanup_probe (principal BIGINT NOT NULL)").Error)
	failure := errors.New("knowledge cleanup unavailable")
	fail := true
	m.retirementObserver = func(ctx context.Context, tx *gorm.DB, tenant, principal, eventID guid.ID) error {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, child.PrincipalID, principal)
		require.Positive(t, int64(eventID))
		if err := tx.WithContext(ctx).Exec("INSERT INTO retirement_cleanup_probe VALUES (?)", principal).Error; err != nil {
			return err
		}
		if fail {
			return failure
		}
		return nil
	}
	retire := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.RetireSpawnedAgentOn(ctx, tx, 7, child.ID, 1, "Retire completed capacity safely")
			return err
		})
	}
	require.ErrorIs(t, retire(), failure)
	q := query.BindAgents(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(child.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
	var count int64
	require.NoError(t, db.Table("retirement_cleanup_probe").Count(&count).Error)
	require.Zero(t, count)
	fail = false
	require.NoError(t, retire())
	row, err = q.WithContext(ctx).Where(q.ID.Eq(child.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "RETIRED", row.State)
	require.NoError(t, db.Table("retirement_cleanup_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestSpawnAndRetireRequireCurrentSourceOwnersBeforeAnyEffects(t *testing.T) {
	for _, command := range []string{"spawn", "retire"} {
		for _, fault := range []string{"missing_lock", "missing_account", "missing_fence", "source_account", "source_fence", "lock", "missing_policy", "source_policy", "identifier", "zero_identifier"} {
			t.Run(command+"/"+fault, func(t *testing.T) {
				m, db, ctx, _, in := agentSpawnHarness(t)
				var child RegisteredAgent
				if command == "retire" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; child, e = m.SpawnAgentOn(ctx, tx, 7, in); return e }))
				}
				q := query.BindAgents(db, m.names)
				before, e := q.WithContext(ctx).Order(q.ID).Find()
				require.NoError(t, e)
				var baselineEffects int64
				require.NoError(t, db.Table("test_fleet_effects").Count(&baselineEffects).Error)
				injected := errors.New("spawn current source unavailable")
				switch fault {
				case "missing_lock":
					m.principals.Lock = nil
				case "missing_account":
					m.accounts = nil
				case "missing_fence":
					m.spawnPolicy.FenceOn = nil
				case "source_account":
					m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, injected }
				case "source_fence":
					m.spawnPolicy.FenceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return injected }
				case "lock":
					m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
				case "missing_policy":
					if command == "spawn" {
						m.spawnPolicy.AuthorizeOn = nil
					} else {
						m.spawnPolicy.RetireOn = nil
					}
				case "source_policy":
					if command == "spawn" {
						m.spawnPolicy.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, AgentSpawnInput, bool) (AgentSpawnGrant, error) {
							return AgentSpawnGrant{}, injected
						}
					} else {
						m.spawnPolicy.RetireOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, bool) error { return injected }
					}
				case "identifier":
					m.ids = failedFleetIDs{err: injected}
				case "zero_identifier":
					m.ids = failedFleetIDs{}
				}
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					if command == "spawn" {
						_, e := m.SpawnAgentOn(ctx, tx, 7, in)
						return e
					}
					_, e := m.RetireSpawnedAgentOn(ctx, tx, 7, child.ID, child.Version, "Retire actual completed idle child")
					return e
				}))
				after, e := q.WithContext(ctx).Order(q.ID).Find()
				require.NoError(t, e)
				require.Equal(t, before, after)
				var effects int64
				require.NoError(t, db.Table("test_fleet_effects").Count(&effects).Error)
				require.Equal(t, baselineEffects, effects)
			})
		}
	}
}
