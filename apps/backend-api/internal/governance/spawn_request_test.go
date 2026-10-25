package governance

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAgentSpawnRequestReconcileOrphanAndExpiryAtomicReplay(t *testing.T) {
	for _, state := range []string{"ORPHANED", "EXPIRED"} {
		t.Run(state, func(t *testing.T) {
			m, db, human, agent, pub, in := agentSpawnFixture(t)
			var row model.SpawnRequest
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				row, err = m.createAgentSpawnRequestOn(agent, tx, 1, in)
				return err
			}))
			require.NoError(t, m.reconcileAgentSpawnRequests(t.Context(), jobs.Job{}))
			require.EqualValues(t, 2, seedCount(t, db, "event_probe"), "grant and request each publish once")
			if state == "ORPHANED" {
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, nil
				}
			} else {
				m.seed.now = func() time.Time { return row.ExpiresAt }
			}
			pub.Fail = true
			require.Error(t, m.reconcileAgentSpawnRequests(t.Context(), jobs.Job{}))
			q := query.BindSpawnRequests(db, m.seed.names)
			saved, err := q.WithContext(human).Where(q.ID.Eq(row.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "REQUESTED", saved.State)
			pub.Fail = false
			require.NoError(t, m.reconcileAgentSpawnRequests(t.Context(), jobs.Job{}))
			require.NoError(t, m.reconcileAgentSpawnRequests(t.Context(), jobs.Job{}))
			saved, err = q.WithContext(human).Where(q.ID.Eq(row.ID)).First()
			require.NoError(t, err)
			require.Equal(t, state, saved.State)
			require.Equal(t, 2, saved.Version)
			require.EqualValues(t, 3, seedCount(t, db, "event_probe"))
			require.EqualValues(t, 3, seedCount(t, db, "audit_probe"))
		})
	}
}

func agentSpawnFixture(t *testing.T) (*Module, *gorm.DB, context.Context, context.Context, *governanceTestPublisher, AgentSpawnRequest) {
	m, db, human, pub, grant := spawnGrantHarness(t)
	_, err := grantSpawn(t, m, db, human, grant)
	require.NoError(t, err)
	m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	m.spawnDirectory.MachineOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
		return fleet.MachineFact{ID: 5, PrincipalID: 6, State: "ONLINE"}, nil
	}
	agent := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 3, SubjectType: authn.SubjectTypePrincipal})
	in := AgentSpawnRequest{Facts: SpawnRequestFacts{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Role: "EXECUTOR", MachineID: 5, Runtime: "CODEX", Model: "model", Effort: "medium", EstimatedCapMicros: 1000000}, Registration: fleet.AgentRegistration{PrincipalID: 44, MachineID: 5, Name: "Proposed executor", Handle: "proposed-executor", Runtime: "CODEX", Model: "model", Roles: []string{"EXECUTOR"}, SystemPrompt: "Implement only the assigned plan."}, Reason: "Request bounded executor capacity"}
	return m, db, human, agent, pub, in
}
func TestAgentSpawnRequestInboxRechecksSourceAuthorityAndCursor(t *testing.T) {
	m, db, human, agent, _, in := agentSpawnFixture(t)
	var row model.SpawnRequest
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		row, err = m.createAgentSpawnRequestOn(agent, tx, 1, in)
		return err
	}))
	req := InboxRequest{TenantID: 1, PrincipalID: 2, Kind: "AGENT_SPAWN_APPROVAL", Limit: 1}
	items, err := m.agentSpawnRequestInbox(human, req)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, row.ID, items[0].ObjectID)
	require.Equal(t, "spawn_request", items[0].ObjectType)
	req.After = &items[0].InboxKey
	items, err = m.agentSpawnRequestInbox(human, req)
	require.NoError(t, err)
	require.Empty(t, items)
	req.After = nil
	req.ProjectID = 99
	items, err = m.agentSpawnRequestInbox(human, req)
	require.NoError(t, err)
	require.Len(t, items, 1)
	req.ProjectID = 100
	items, err = m.agentSpawnRequestInbox(human, req)
	require.NoError(t, err)
	require.Empty(t, items)
	req.ProjectID = 0
	explain := m.explain
	m.explain = func(ctx context.Context, tx *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		if r.Permission == "project_read" {
			return authz.Explanation{Allowed: false}, nil
		}
		return explain(ctx, tx, r)
	}
	items, err = m.agentSpawnRequestInbox(human, req)
	require.NoError(t, err)
	require.Empty(t, items)
	m.explain = explain
	g := query.BindSpawnGrants(db, m.seed.names)
	grant, err := g.WithContext(human).Where(g.TenantID.Eq(guid.ID(1))).First()
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.revokeSpawnGrantOn(human, tx, 1, grant.ID, grant.Version, "Withdraw pending spawn authorization")
		return err
	}))
	items, err = m.agentSpawnRequestInbox(human, req)
	require.NoError(t, err)
	require.Empty(t, items)
	m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, nil
	}
	items, err = m.agentSpawnRequestInbox(human, req)
	require.NoError(t, err)
	require.Empty(t, items)
	req.PrincipalID = 3
	items, err = m.agentSpawnRequestInbox(agent, req)
	require.NoError(t, err)
	require.Empty(t, items)
	req.TenantID = 2
	_, err = m.agentSpawnRequestInbox(human, req)
	require.ErrorIs(t, err, ErrForbidden)
}
func TestAgentSpawnRequestSourceGrantHumanAdoptionAndAtomicRegistration(t *testing.T) {
	m, db, human, agent, pub, in := agentSpawnFixture(t)
	var row model.SpawnRequest
	create := func() error {
		return db.Transaction(func(tx *gorm.DB) error { var e error; row, e = m.createAgentSpawnRequestOn(agent, tx, 1, in); return e })
	}
	require.NoError(t, create())
	require.Equal(t, "REQUESTED", row.State)
	require.Nil(t, row.AgentID)
	require.NoError(t, db.Exec("CREATE TABLE registered_probe(id BIGINT)").Error)
	calls := 0
	m.spawnRegistration = fleet.AgentRegistrationCommands{RegisterOn: func(ctx context.Context, tx *gorm.DB, tenant guid.ID, registration fleet.AgentRegistration) (fleet.RegisteredAgent, error) {
		calls++
		require.EqualValues(t, 2, authn.PrincipalIDFromContext(ctx))
		require.EqualValues(t, 1, tenant)
		return fleet.RegisteredAgent{ID: 77, PrincipalID: registration.PrincipalID, MachineID: registration.MachineID}, tx.Exec("INSERT INTO registered_probe VALUES (77)").Error
	}}
	change := func(ctx context.Context, version int, action string) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var e error
			row, e = m.changeAgentSpawnRequestOn(ctx, tx, 1, row.ID, version, action, "Reviewed current source and request")
			return e
		})
	}
	id := row.ID
	require.ErrorIs(t, change(agent, 1, "ADOPT"), ErrForbidden)
	row.ID = id
	pub.Fail = true
	require.Error(t, change(human, 1, "ADOPT"))
	require.Equal(t, 1, calls)
	require.Zero(t, seedCount(t, db, "registered_probe"))
	q := query.BindSpawnRequests(db, m.seed.names)
	saved, e := q.WithContext(human).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Equal(t, "REQUESTED", saved.State)
	row.ID = id
	pub.Fail = false
	require.NoError(t, change(human, 1, "ADOPT"))
	require.Equal(t, "ADOPTED", row.State)
	require.EqualValues(t, 77, *row.AgentID)
	require.EqualValues(t, 1, seedCount(t, db, "registered_probe"))
	row.ID = id
	require.ErrorIs(t, change(human, 2, "ADOPT"), ErrVersion)
}

func TestAgentSpawnRequestPendingProposalsConsumeCurrentGrantCapacity(t *testing.T) {
	m, db, human, agent, _, in := agentSpawnFixture(t)
	var row model.SpawnRequest
	create := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			var err error
			row, err = m.createAgentSpawnRequestOn(agent, tx, 1, in)
			return err
		})
	}
	require.NoError(t, create())
	id := row.ID
	require.ErrorIs(t, create(), ErrForbidden, "the grant permits only one unresolved proposal or child")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.changeAgentSpawnRequestOn(human, tx, 1, id, 1, "REJECT", "Rejected pending capacity request")
		return err
	}))
	require.NoError(t, create(), "terminal requests release pending capacity without inventing a child reservation")
}
func TestAgentSpawnRequestUnavailableCreateAndSourceBackedOrphan(t *testing.T) {
	m, db, human, agent, _, in := agentSpawnFixture(t)
	var row model.SpawnRequest
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; row, e = m.createAgentSpawnRequestOn(agent, tx, 1, in); return e }))
	change := func(version int, action string) error {
		return db.Transaction(func(tx *gorm.DB) error {
			next, e := m.changeAgentSpawnRequestOn(human, tx, 1, row.ID, version, action, "Current source state checked")
			if e == nil {
				row = next
			}
			return e
		})
	}
	require.ErrorIs(t, change(1, "ORPHAN"), ErrInvalidArgument)
	require.NoError(t, change(1, "CREATE"))
	require.Equal(t, "UNAVAILABLE", row.State)
	require.Nil(t, row.AgentID)
	m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, nil
	}
	require.NoError(t, change(2, "ORPHAN"))
	require.Equal(t, "ORPHANED", row.State)
	require.Nil(t, row.AgentID)
}

func TestAgentSpawnRequestCreationFailuresLeaveNoProposalOrEffects(t *testing.T) {
	for _, scenario := range []string{"reason", "actor", "unavailable", "identifier", "zero_identifier", "insert", "audit", "grant_read", "pending_read"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _, ctx, _, in := agentSpawnFixture(t)
			fault := errors.New("request source failed")
			want := fault
			eventsBefore := seedCount(t, db, "event_probe")
			auditBefore := seedCount(t, db, "audit_probe")
			switch scenario {
			case "reason":
				in.Reason = "short"
				want = ErrInvalidArgument
			case "actor":
				ctx = t.Context()
				want = ErrForbidden
			case "unavailable":
				m.spawnFence = nil
				want = ErrUnavailable
			case "identifier":
				m.seed.ids = seedTestIDs{err: fault}
			case "zero_identifier":
				m.seed.ids = seedTestIDs{}
				want = ErrUnavailable
			case "insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:proposal", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.SpawnRequests.String() {
						tx.AddError(fault)
					}
				}))
			case "audit":
				m.seed.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "grant_read", "pending_read":
				table := m.seed.names.SpawnGrants.String()
				if scenario == "pending_read" {
					table = m.seed.names.SpawnRequests.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:proposal", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, err := m.createAgentSpawnRequestOn(ctx, tx, 1, in); return err })
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Create().Remove("test:proposal"))
			require.NoError(t, db.Callback().Query().Remove("test:proposal"))
			require.Zero(t, seedCount(t, db, m.seed.names.SpawnRequests.String()))
			require.Equal(t, eventsBefore, seedCount(t, db, "event_probe"))
			require.Equal(t, auditBefore, seedCount(t, db, "audit_probe"))
		})
	}
}
func TestAgentSpawnRequestChangeRechecksSourcesAndRollsBack(t *testing.T) {
	for _, scenario := range []string{"payload", "lock_missing", "lock_error", "machine_missing", "machine_error", "supervisor_missing", "supervisor_error", "registration_missing", "registration_error", "registration_identity", "update", "audit", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, human, agent, _, in := agentSpawnFixture(t)
			var row model.SpawnRequest
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				row, err = m.createAgentSpawnRequestOn(agent, tx, 1, in)
				return err
			}))
			fault := errors.New("request change failed")
			want := fault
			action := "ADOPT"
			table := m.seed.names.SpawnRequests.String()
			switch scenario {
			case "payload":
				require.NoError(t, db.Table(table).Where("id=?", row.ID).Update("payload", "{}").Error)
				want = ErrUnavailable
			case "lock_missing":
				m.principals.Lock = nil
				want = ErrUnavailable
			case "lock_error":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return fault }
			case "machine_missing":
				m.spawnDirectory.MachineOn = nil
				want = ErrUnavailable
			case "machine_error":
				m.spawnDirectory.MachineOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, fault
				}
			case "supervisor_missing":
				m.spawnDirectory.AgentOn = nil
				want = ErrUnavailable
			case "supervisor_error":
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, fault
				}
			case "registration_missing":
				m.spawnRegistration.RegisterOn = nil
				want = ErrUnavailable
			case "registration_error":
				m.spawnRegistration.RegisterOn = func(context.Context, *gorm.DB, guid.ID, fleet.AgentRegistration) (fleet.RegisteredAgent, error) {
					return fleet.RegisteredAgent{}, fault
				}
			case "registration_identity":
				m.spawnRegistration.RegisterOn = func(context.Context, *gorm.DB, guid.ID, fleet.AgentRegistration) (fleet.RegisteredAgent, error) {
					return fleet.RegisteredAgent{ID: 77, PrincipalID: 45, MachineID: 5}, nil
				}
				want = ErrUnavailable
			case "update":
				action = "REJECT"
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:request-change", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
			case "audit":
				action = "REJECT"
				m.seed.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "expired":
				m.seed.now = func() time.Time { return row.ExpiresAt }
				action = "REJECT"
				want = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				out, e := m.changeAgentSpawnRequestOn(human, tx, 1, row.ID, 1, action, "Reviewed request against current sources")
				if scenario == "expired" && e == nil {
					require.Equal(t, "EXPIRED", out.State)
				}
				return e
			})
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Update().Remove("test:request-change"))
			q := query.BindSpawnRequests(db, m.seed.names)
			saved, e := q.WithContext(human).Where(q.ID.Eq(row.ID)).First()
			require.NoError(t, e)
			if want != nil {
				require.Equal(t, "REQUESTED", saved.State)
				require.Equal(t, 1, saved.Version)
				require.Nil(t, saved.AgentID)
			} else {
				require.Equal(t, "EXPIRED", saved.State)
				require.Equal(t, 2, saved.Version)
			}
		})
	}
}

func TestAgentSpawnInboxCurrentSourceFailuresCannotExposeRequest(t *testing.T) {
	for _, scenario := range []string{"human", "lock", "inactive_lock", "unavailable", "query", "payload", "grant_query", "scope", "spent", "project", "supervisor_error", "supervisor_inactive"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, human, agent, _, in := agentSpawnFixture(t)
			var row model.SpawnRequest
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				row, err = m.createAgentSpawnRequestOn(agent, tx, 1, in)
				return err
			}))
			fault := errors.New("pending request source failed")
			want := fault
			empty := false
			switch scenario {
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "lock":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "inactive_lock":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				want = nil
				empty = true
			case "unavailable":
				m.spawnDirectory.AgentOn = nil
				want = ErrUnavailable
			case "query", "grant_query":
				table := m.seed.names.SpawnRequests.String()
				if scenario == "grant_query" {
					table = m.seed.names.SpawnGrants.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:spawn-inbox", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
			case "payload":
				require.NoError(t, db.Table(m.seed.names.SpawnRequests.String()).Where("id=?", row.ID).Update("payload", "{}").Error)
				want = ErrUnavailable
			case "scope":
				require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("tenant_id=?", 1).Update("scope_json", "{}").Error)
				want = ErrUnavailable
			case "spent":
				require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("tenant_id=?", 1).Update("spent_micros", 2000000).Error)
				want = nil
				empty = true
			case "project":
				m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					if r.Permission == "project_read" {
						return authz.Explanation{}, fault
					}
					return authz.Explanation{Allowed: true}, nil
				}
			case "supervisor_error":
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, fault
				}
			case "supervisor_inactive":
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, nil
				}
				want = nil
				empty = true
			}
			rows, err := m.agentSpawnRequestInbox(human, InboxRequest{TenantID: 1, PrincipalID: 2, Kind: "AGENT_SPAWN_APPROVAL", Limit: 10})
			require.ErrorIs(t, err, want)
			if empty || want != nil {
				require.Empty(t, rows)
			}
		})
	}
}
func TestAgentSpawnReconciliationSourceFailurePreservesUnresolvedRequest(t *testing.T) {
	for _, scenario := range []string{"lock", "supervisor", "query", "update", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, agent, _, in := agentSpawnFixture(t)
			var row model.SpawnRequest
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				row, err = m.createAgentSpawnRequestOn(agent, tx, 1, in)
				return err
			}))
			fault := errors.New("reconcile source failed")
			want := fault
			switch scenario {
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return fault }
			case "supervisor":
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, fault
				}
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:reconcile", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.SpawnRequests.String() {
						tx.AddError(fault)
					}
				}))
			case "update":
				m.seed.now = func() time.Time { return row.ExpiresAt }
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:reconcile", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.SpawnRequests.String() {
						tx.AddError(fault)
					}
				}))
			case "unavailable":
				m.principals.Lock = nil
				want = ErrUnavailable
			}
			require.ErrorIs(t, m.reconcileAgentSpawnRequests(t.Context(), jobs.Job{}), want)
			require.NoError(t, db.Callback().Query().Remove("test:reconcile"))
			require.NoError(t, db.Callback().Update().Remove("test:reconcile"))
			q := query.BindSpawnRequests(db, m.seed.names)
			saved, err := q.WithContext(ctx).Where(q.ID.Eq(row.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "REQUESTED", saved.State)
			require.Equal(t, 1, saved.Version)
		})
	}
}
