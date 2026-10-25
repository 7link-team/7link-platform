package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/infra/realtime"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGovernancePublicationRejectsConflictingCapabilities(t *testing.T) {
	for name, provide := range map[string]func(*module.Services) error{
		"policy":     func(s *module.Services) error { return module.Provide[PolicyRegistry](s, &policyRegistry{}) },
		"spawn_cost": func(s *module.Services) error { return module.Provide[SpawnCostConnector](s, SpawnCostConnector{}) },
		"archive_facts": func(s *module.Services) error {
			return module.Provide[ProjectArchiveFacts](s, (&Module{}).projectArchiveFactsOn)
		},
		"andon": func(s *module.Services) error { return module.Provide[AndonAuthority](s, (&Module{}).andonAuthorityOn) },
		"terminal": func(s *module.Services) error {
			return module.Provide[ProjectTerminalCleanup](s, ProjectTerminalCleanup{})
		},
		"archive": func(s *module.Services) error { return module.Provide[EscapeEvalConnector](s, EscapeEvalConnector{}) },
		"mode": func(s *module.Services) error {
			return module.Provide[ProjectModeGovernance](s, ProjectModeGovernance{})
		},
		"spawn_facts": func(s *module.Services) error {
			return module.Provide[SpawnNotificationFacts](s, (&Module{}).spawnNotificationFacts)
		},
		"incident_facts": func(s *module.Services) error {
			return module.Provide[IncidentNotificationFacts](s, (&Module{}).incidentNotificationFacts)
		},
		"reviews": func(s *module.Services) error {
			return module.Provide[QualityReviewConnector](s, QualityReviewConnector{})
		},
		"applied": func(s *module.Services) error {
			return module.Provide[AppliedDelegations](s, (&Module{}).appliedDelegationsOn)
		},
		"lead": func(s *module.Services) error { return module.Provide[ProjectLead](s, (&Module{}).projectLeadOn) },
		"automated": func(s *module.Services) error {
			return module.Provide[AutomatedDecisionConnector](s, AutomatedDecisionConnector{})
		},
		"delegation_facts": func(s *module.Services) error {
			return module.Provide[DelegationNotificationFacts](s, (&Module{}).delegationNotificationFacts)
		},
		"usage": func(s *module.Services) error { return module.Provide[SpawnGrantUsage](s, SpawnGrantUsage{}) },
		"claim": func(s *module.Services) error {
			return module.Provide[ClaimEvaluationConnector](s, ClaimEvaluationConnector{})
		},
		"projects": func(s *module.Services) error {
			return module.Provide[DelegationProjectConnector](s, DelegationProjectConnector{})
		},
		"workflow": func(s *module.Services) error { return module.Provide[WorkflowDecisions](s, WorkflowDecisions{}) },
		"settings": func(s *module.Services) error { return module.Provide[SettingsReader](s, (&Module{}).readSettingsOn) },
		"escalation": func(s *module.Services) error {
			return module.Provide[EscalationRecorder](s, (&Module{}).recordEscalationOn)
		},
		"inbox": func(s *module.Services) error { return module.Provide[InboxSources](s, &inboxRegistry{}) },
		"delivery": func(s *module.Services) error {
			return module.Provide[DeliveryAuthority](s, (&Module{}).deliveryAuthority)
		},
		"stage": func(s *module.Services) error {
			return module.Provide[ProjectStageAuthority](s, (&Module{}).projectStageAuthority)
		},
		"creation": func(s *module.Services) error {
			return module.Provide[ProjectCreationAuthority](s, (&Module{}).projectCreationAuthority)
		},
		"roles":   func(s *module.Services) error { return module.Provide[SeededRoles](s, SeededRoles{}) },
		"actions": func(s *module.Services) error { return module.Provide[SystemActions](s, &systemActionRegistry{}) },
		"allowed": func(s *module.Services) error {
			return module.Provide[SystemActionAllowed](s, (&systemActionRegistry{}).allows)
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
			s := module.NewServices()
			require.NoError(t, provide(s))
			require.ErrorIs(t, m.ProvideServices(s), module.ErrServiceDuplicate)
		})
	}
}

func seedProviders(s *seedService) []func(*module.Services) error {
	return []func(*module.Services) error{
		func(v *module.Services) error { return module.Provide[fleet.AgentDirectory](v, fleet.AgentDirectory{}) },
		func(v *module.Services) error {
			return module.Provide[fleet.SpawnGrantDirectory](v, fleet.SpawnGrantDirectory{})
		},
		func(v *module.Services) error {
			return module.Provide[dlock.TransactionGuard](v, func(context.Context, *gorm.DB, string) error { return nil })
		},
		func(v *module.Services) error {
			return module.Provide[core.ActiveMemberChecker](v, delegationMembers{true})
		},
		func(v *module.Services) error {
			return module.Provide[core.EntityRoleBindingStore](v, core.EntityRoleBindingStore{})
		},
		func(v *module.Services) error {
			return module.Provide[core.EntityRoleBindingRevoker](v, core.EntityRoleBindingRevoker(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error) { return false, nil }))
		},
		func(v *module.Services) error { return module.Provide[idempotency.Store](v, delegationReplayFixture{}) },
		func(v *module.Services) error {
			return module.Provide[identity.PrincipalStateStore](v, identity.PrincipalStateStore{})
		},
		func(v *module.Services) error { return module.Provide[events.Publisher](v, moduleGovernanceEvents{}) },
		func(v *module.Services) error { return module.Provide[events.Catalog](v, moduleGovernanceEvents{}) },
		func(v *module.Services) error {
			return module.Provide[events.SubscriberCatalog](v, moduleGovernanceEvents{})
		},
		func(v *module.Services) error {
			return module.Provide[authn.ReauthenticationConsumer](v, func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil })
		},
		func(v *module.Services) error { return module.Provide[jobs.Queue](v, &seedJobQueue{}) },
		func(v *module.Services) error { return module.Provide[guid.Source](v, s.ids) },
		func(v *module.Services) error { return module.Provide[core.RoleSeeder](v, s.roles) },
		func(v *module.Services) error {
			return module.Provide[core.AuthorizationExplanation](v, core.AuthorizationExplanation(func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{}, nil
			}))
		},
		func(v *module.Services) error { return module.Provide[audit.Appender](v, s.audit) },
		func(v *module.Services) error {
			return module.Provide[audit.BusinessReader](v, audit.BusinessReader(func(context.Context, *gorm.DB, audit.BusinessQuery, func(audit.BusinessEvent) (bool, error)) ([]audit.BusinessEvent, int64, error) {
				return nil, 0, ErrUnavailable
			}))
		},
		func(v *module.Services) error { return module.Provide[dlock.TransactionLease](v, s.acquire) },
		func(v *module.Services) error {
			return module.Provide[organization.TenantEnumerator](v, organization.TenantEnumerator(func(context.Context, guid.ID, int) ([]guid.ID, error) { return nil, nil }))
		},
		func(v *module.Services) error {
			return module.Provide[*authz.Registrar](v, authz.NewUnboundRegistrar(s.registry))
		},
	}
}

func TestModulePublishesSeededRolesAndPreservesDetachedRoleIdentity(t *testing.T) {
	s, db, rt := seedHarnessRuntime(t)
	seedLocks(t, s, db)
	s.roles.SeedAdministratorMembersOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{}, nil }
	s.roles.GrantConditionalPermissionOn = func(ctx context.Context, tx *gorm.DB, tenant, role guid.ID, code string, _ json.RawMessage) (bool, error) {
		return s.roles.GrantPermissionOn(ctx, tx, tenant, role, code)
	}
	m := NewModule(rt, Config{Config: module.Config{Enabled: true}})
	require.True(t, m.Enabled())
	require.Equal(t, Name, m.ModuleName())
	require.ElementsMatch(t, []string{platformi18n.Name, guid.Name, core.Name, audit.Name, dlock.Name, organization.Name, jobs.Name, identity.Name, events.Name, iamauthn.Name, fleet.Name, idempotency.Name, knowledge.Name, intake.Name, realtime.Name}, m.ModuleDependencies())
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.Error(t, m.ProvideServices(services))
	for _, provide := range seedProviders(s) {
		require.NoError(t, provide(services))
	}
	require.NoError(t, services.Seal())
	roles, err := module.Resolve[SeededRoles](services)
	require.NoError(t, err)
	creation, err := module.Resolve[ProjectCreationAuthority](services)
	require.NoError(t, err)
	stage, err := module.Resolve[ProjectStageAuthority](services)
	require.NoError(t, err)
	stageDecision, err := stage(t.Context(), nil, 0, 99, 11, "HUMAN")
	require.NoError(t, err)
	require.False(t, stageDecision.Allowed)
	require.Equal(t, "INVALID_REQUEST", stageDecision.ReasonCode)
	actual, err := creation(t.Context(), nil, 0, 11, "HUMAN")
	require.NoError(t, err)
	expected, err := m.Check(t.Context(), nil, AuthorityRequest{TenantID: 0, PrincipalID: 11, ActorKind: "HUMAN", Code: "project_create"})
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	require.ErrorIs(t, roles.EnsureTenant(t.Context(), 1), ErrUnavailable)
	systemActions, err := module.Resolve[SystemActions](services)
	require.NoError(t, err)
	require.NoError(t, systemActions.Register("test.seed", "user_view"))
	policies, err := module.Resolve[PolicyRegistry](services)
	require.NoError(t, err)
	require.NoError(t, policies.Register(PolicyRule{ID: "test", Codes: []string{"user_view"}, Evaluate: func(context.Context, AuthorityRequest) (bool, error) { return true, nil }}))
	require.False(t, m.systemActions.allows("test.seed", "user_view"))
	require.NoError(t, m.InjectServices(services))
	require.NoError(t, m.Initialize(t.Context()))
	require.True(t, m.systemActions.allows("test.seed", "user_view"))
	outcome, err := m.policies.evaluate(t.Context(), AuthorityRequest{Code: "user_view"})
	require.NoError(t, err)
	require.Equal(t, "POLICY:test", outcome.source)
	require.Error(t, systemActions.Register("test.late", "role_view"))
	m.tenants = func(context.Context, guid.ID, int) ([]guid.ID, error) { return []guid.ID{1}, nil }
	require.NoError(t, m.JobHandlers()[0].Run(t.Context(), jobs.Job{}))
	require.NoError(t, roles.EnsureTenant(t.Context(), 1))
	require.EqualValues(t, 15, seedCount(t, db, "g_governance_role_seeds"))
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
	var engineer guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		engineer, err = roles.RoleIDOn(t.Context(), tx, 1, "engineer")
		require.NoError(t, err)
		require.Positive(t, int64(engineer))
		_, err = roles.RoleIDOn(t.Context(), tx, 2, "engineer")
		require.ErrorIs(t, err, ErrRoleNotSeeded)
		_, err = roles.RoleIDOn(t.Context(), tx, 1, "not_a_role")
		require.ErrorIs(t, err, ErrRoleNotSeeded)
		return nil
	}))
	require.NoError(t, db.Exec("UPDATE g_governance_role_seeds SET managed=false WHERE role_key='engineer'").Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		id, err := roles.RoleIDOn(t.Context(), tx, 1, "engineer")
		require.NoError(t, err)
		require.Equal(t, engineer, id)
		return nil
	}))
	require.NoError(t, db.Exec("UPDATE g_governance_role_seeds SET seed_conflict=true WHERE role_key='engineer'").Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := roles.RoleIDOn(t.Context(), tx, 1, "engineer")
		require.ErrorIs(t, err, ErrRoleNotSeeded)
		return nil
	}))
	_, err = roles.RoleIDOn(t.Context(), db, 1, "engineer")
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = roles.RoleIDOn(t.Context(), nil, 1, "engineer")
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := roles.RoleIDOn(t.Context(), tx, 0, "engineer")
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = roles.RoleIDOn(t.Context(), tx, 1, "")
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
	require.NoError(t, db.Exec("DROP TABLE g_governance_role_seeds").Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := roles.RoleIDOn(t.Context(), tx, 1, "engineer"); return err }))
	m.runtime = nil
	require.ErrorIs(t, roles.EnsureTenant(t.Context(), 1), ErrUnavailable)
	m.runtime = appenv.NewRuntime(appenv.NewIdentity("test", "1"))
	require.ErrorIs(t, roles.EnsureTenant(t.Context(), 1), ErrUnavailable)
}

func TestModuleRejectsMissingDependencies(t *testing.T) {
	s, db, rt := seedHarnessRuntime(t)
	seedLocks(t, s, db)
	for missing := range seedProviders(s) {
		m := NewModule(rt, Config{})
		services := module.NewServices()
		for index, provide := range seedProviders(s) {
			if index != missing {
				require.NoError(t, provide(services))
			}
		}
		require.NoError(t, services.Seal())
		require.Error(t, m.InjectServices(services))
	}
	m := NewModule(rt, Config{})
	require.False(t, m.Enabled())
	require.False(t, (*Module)(nil).Enabled())
	for _, owner := range []*Module{nil, {}, m} {
		require.ErrorIs(t, owner.Initialize(t.Context()), ErrUnavailable)
		if owner != m {
			_, err := owner.roleIDOn(t.Context(), nil, 1, "engineer")
			require.ErrorIs(t, err, ErrUnavailable)
		}
	}
	require.ErrorIs(t, (&Module{}).InjectServices(module.NewServices()), ErrUnavailable)
	require.ErrorIs(t, (*Module)(nil).InjectServices(module.NewServices()), ErrUnavailable)
	services := module.NewServices()
	providers := seedProviders(s)
	for _, provide := range providers[:len(providers)-1] {
		require.NoError(t, provide(services))
	}
	require.NoError(t, module.Provide[*authz.Registrar](services, &authz.Registrar{}))
	require.NoError(t, services.Seal())
	require.NoError(t, m.InjectServices(services))
	require.ErrorIs(t, m.Initialize(t.Context()), ErrUnavailable)
}

func TestReconcileTenantsPagesAndRetriesIdempotently(t *testing.T) {
	s, db, rt := seedHarnessRuntime(t)
	seedLocks(t, s, db)
	s.roles.SeedAdministratorMembersOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }
	s.roles.GrantConditionalPermissionOn = func(ctx context.Context, tx *gorm.DB, tenant, role guid.ID, code string, _ json.RawMessage) (bool, error) {
		return s.roles.GrantPermissionOn(ctx, tx, tenant, role, code)
	}
	m := &Module{runtime: rt, seed: s, explain: seedOnlyExplanation}
	var cursors []guid.ID
	m.tenants = func(_ context.Context, after guid.ID, limit int) ([]guid.ID, error) {
		cursors = append(cursors, after)
		require.Equal(t, 100, limit)
		var ids []guid.ID
		for id := after + 1; id <= 101 && len(ids) < limit; id++ {
			ids = append(ids, id)
		}
		return ids, nil
	}
	for range 2 {
		require.NoError(t, m.reconcileTenants(t.Context()))
	}
	require.Equal(t, []guid.ID{0, 100, 0, 100}, cursors)
	require.EqualValues(t, 101*15, seedCount(t, db, "g_governance_role_seeds"))
	require.EqualValues(t, 101*15, seedCount(t, db, "roles_probe"))
	require.EqualValues(t, 101, seedCount(t, db, "audit_probe"))
	var lastTenantRoles int64
	require.NoError(t, db.Table("roles_probe").Where("tenant_id = ?", 101).Count(&lastTenantRoles).Error)
	require.EqualValues(t, 15, lastTenantRoles)
}

func TestReconcileTenantsLockContentionFailureAndCancellation(t *testing.T) {
	s, db, rt := seedHarnessRuntime(t)
	seedLocks(t, s, db)
	s.roles.SeedAdministratorMembersOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }
	s.roles.GrantConditionalPermissionOn = func(ctx context.Context, tx *gorm.DB, tenant, role guid.ID, code string, _ json.RawMessage) (bool, error) {
		return s.roles.GrantPermissionOn(ctx, tx, tenant, role, code)
	}
	m := &Module{runtime: rt, seed: s, explain: seedOnlyExplanation, tenants: func(context.Context, guid.ID, int) ([]guid.ID, error) { return []guid.ID{1, 2, 3}, nil }}
	lease, err := s.acquire(t.Context(), "governance:seed:1", time.Minute)
	require.NoError(t, err)
	require.ErrorIs(t, m.ensureTenant(t.Context(), 1), dlock.ErrNotAcquired)
	require.NoError(t, m.reconcileTenants(t.Context()))
	require.EqualValues(t, 30, seedCount(t, db, "g_governance_role_seeds"))
	require.NoError(t, lease.Unlock(t.Context()))
	require.NoError(t, m.reconcileTenants(t.Context()))
	require.EqualValues(t, 45, seedCount(t, db, "g_governance_role_seeds"))
	failure := errors.New("audit unavailable")
	originalAudit := s.audit
	s.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		if event.TenantID == 5 {
			return failure
		}
		return originalAudit(ctx, tx, event)
	}
	m.tenants = func(context.Context, guid.ID, int) ([]guid.ID, error) { return []guid.ID{4, 5, 6}, nil }
	require.ErrorIs(t, m.reconcileTenants(t.Context()), failure)
	require.EqualValues(t, 60, seedCount(t, db, "g_governance_role_seeds"))
	require.EqualValues(t, 60, seedCount(t, db, "roles_probe"), "failed tenant must roll back; later tenants wait for retry")
	s.audit = originalAudit
	require.NoError(t, m.reconcileTenants(t.Context()))
	require.EqualValues(t, 90, seedCount(t, db, "g_governance_role_seeds"))
	require.EqualValues(t, 6, seedCount(t, db, "audit_probe"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, m.reconcileTenants(ctx), context.Canceled)
	m.tenants = func(context.Context, guid.ID, int) ([]guid.ID, error) { return nil, failure }
	require.ErrorIs(t, m.reconcileTenants(t.Context()), failure)
	ctx, cancel = context.WithCancel(t.Context())
	m.tenants = func(context.Context, guid.ID, int) ([]guid.ID, error) { cancel(); return []guid.ID{7}, nil }
	require.ErrorIs(t, m.reconcileTenants(ctx), context.Canceled)
	require.EqualValues(t, 90, seedCount(t, db, "g_governance_role_seeds"))
	m.tenants = func(context.Context, guid.ID, int) ([]guid.ID, error) { return nil, nil }
	require.NoError(t, m.reconcileTenants(t.Context()))
	require.ErrorIs(t, (*Module)(nil).reconcileTenants(t.Context()), ErrUnavailable)
	require.ErrorIs(t, (&Module{}).reconcileTenants(t.Context()), ErrUnavailable)
}

func TestRoleReconciliationJobAndSchedule(t *testing.T) {
	m := &Module{}
	handlers := m.JobHandlers()
	require.Len(t, handlers, 5)
	require.Equal(t, "governance.delegation_activation", handlers[4].Kind)
	require.ErrorIs(t, handlers[4].Run(t.Context(), jobs.Job{TenantID: 1}), ErrInvalidArgument)
	require.ErrorIs(t, handlers[4].Run(t.Context(), jobs.Job{}), ErrUnavailable)
	require.Equal(t, "governance.spawn_request_reconcile", handlers[3].Kind)
	require.ErrorIs(t, handlers[3].Run(t.Context(), jobs.Job{TenantID: 1}), ErrInvalidArgument)
	require.Equal(t, "governance.spawn_grant_reconcile", handlers[2].Kind)
	require.ErrorIs(t, handlers[2].Run(t.Context(), jobs.Job{TenantID: 1}), ErrInvalidArgument)
	require.Equal(t, "governance.seed_reconcile", handlers[0].Kind)
	require.ErrorIs(t, handlers[0].Run(t.Context(), jobs.Job{TenantID: 1}), ErrInvalidArgument)
	require.ErrorIs(t, handlers[0].Run(t.Context(), jobs.Job{}), ErrUnavailable)
	calls := 0
	m.tenants = func(ctx context.Context, after guid.ID, limit int) ([]guid.ID, error) {
		calls++
		require.Zero(t, after)
		require.Equal(t, 100, limit)
		return nil, nil
	}
	require.NoError(t, handlers[0].Run(t.Context(), jobs.Job{}))
	require.Equal(t, 1, calls)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, handlers[0].Run(ctx, jobs.Job{}), context.Canceled)
	require.Equal(t, 1, calls)
	schedules := m.JobSchedules()
	require.Len(t, schedules, 5)
	require.Equal(t, handlers[4].Kind, schedules[4].Kind)
	require.Equal(t, 30*time.Second, schedules[4].Interval)
	require.Equal(t, jobs.ScopePlatform, schedules[4].TenantScope)
	require.Equal(t, handlers[3].Kind, schedules[3].Kind)
	require.Equal(t, 30*time.Second, schedules[3].Interval)
	require.Equal(t, jobs.ScopePlatform, schedules[3].TenantScope)
	require.Equal(t, handlers[2].Kind, schedules[2].Kind)
	require.Equal(t, 30*time.Second, schedules[2].Interval)
	require.Equal(t, jobs.ScopePlatform, schedules[2].TenantScope)
	require.Equal(t, handlers[0].Kind, schedules[0].Kind)
	require.Equal(t, "governance.seed_reconcile", schedules[0].Name)
	require.Equal(t, time.Hour, schedules[0].Interval)
	require.Equal(t, jobs.ScopePlatform, schedules[0].TenantScope)
}

type seedJobQueue struct {
	jobs.Queue
	request jobs.EnqueueRequest
	err     error
}

func (q *seedJobQueue) Enqueue(_ context.Context, tx *gorm.DB, request jobs.EnqueueRequest) (jobs.EnqueueResult, error) {
	if tx != nil {
		return jobs.EnqueueResult{}, errors.New("unexpected caller transaction")
	}
	q.request = request
	return jobs.EnqueueResult{JobID: 1, Created: true}, q.err
}
func TestModuleStartupPersistsReconciliation(t *testing.T) {
	require.ErrorIs(t, (*Module)(nil).Start(t.Context()), ErrUnavailable)
	require.ErrorIs(t, (&Module{}).Start(t.Context()), ErrUnavailable)
	queue := &seedJobQueue{}
	m := &Module{queue: queue}
	require.NoError(t, m.Start(t.Context()))
	require.Equal(t, jobs.EnqueueRequest{Kind: "governance.seed_reconcile", ConcurrencyKey: "governance.seed_reconcile", IdempotencyKey: fmt.Sprintf("startup:manifest:%d", RoleManifestVersion)}, queue.request)
	queue.err = jobs.ErrQueueFull
	require.NoError(t, m.Start(t.Context()), "queue saturation must allow workers to start")
	require.Equal(t, "governance.seed_reconcile", m.JobSchedules()[0].Kind, "the durable hourly schedule retries reconciliation")
	queue.err = errors.New("queue storage failed")
	require.ErrorIs(t, m.Start(t.Context()), queue.err)
}

// Role seeding requires a wired module but must not use request authorization.
func seedOnlyExplanation(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
	return authz.Explanation{}, errors.New("unexpected authority check during role seeding")
}

func TestGovernancePublicationRejectsDuplicateInboxSource(t *testing.T) {
	for _, kind := range []string{"MEMORY_RULE_APPROVAL", "ESCALATION_ALERT", "AGENT_SPAWN_APPROVAL", "QUALITY_ESCAPE"} {
		t.Run(kind, func(t *testing.T) {
			m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
			require.NoError(t, m.inbox.Register(inboxFixtureSource(kind, nil)))
			require.ErrorIs(t, m.ProvideServices(module.NewServices()), ErrInvalidArgument)
		})
	}
}
