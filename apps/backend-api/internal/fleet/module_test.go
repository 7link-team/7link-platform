package fleet

import (
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestMachineModuleLifecycleAndRealOutbox(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	require.NoError(t, platformi18n.NewModule(nil, "en-US").Initialize(ctx))
	eventModule := events.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")))
	raw, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(ctx, raw, eventModule.MigrationSource(), "sqlite"))
	services := module.NewServices()
	fm := files.NewModule(m.runtime, files.Config{LocalRoot: t.TempDir()})
	require.NoError(t, fm.ProvideServices(services))
	require.NoError(t, module.Provide[core.AuthorizationExplanation](services, func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true}, nil
	}))
	require.NoError(t, module.Provide[dlock.TryRenewingLease](services, func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
		return nil, ErrUnavailable
	}))
	require.NoError(t, module.Provide[dlock.TransactionGuard](services, func(context.Context, *gorm.DB, string) error { return nil }))
	require.NoError(t, module.Provide[authn.ReauthenticationConsumer](services, authn.ReauthenticationConsumer(func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil })))
	require.NoError(t, module.Provide[identity.ServiceAccountDisabler](services, identity.ServiceAccountDisabler(func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil })))
	require.NoError(t, module.Provide[authn.BrowserSessionSecurity](services, authn.BrowserSessionSecurity{}))
	require.NoError(t, module.Provide[idempotency.Store](services, idempotency.NewModule(m.runtime)))
	require.NoError(t, module.Provide[dlock.RenewingLease](services, func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
		return nil, ErrUnavailable
	}))
	require.NoError(t, module.Provide[EnrollmentIAM](services, EnrollmentIAM{}))
	require.NoError(t, module.Provide[core.TenantConstraintOn](services, func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}))
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, module.Provide[*authz.Registrar](services, authz.NewUnboundRegistrar(registry)))
	require.NoError(t, module.Provide[guid.Source](services, m.ids))
	require.NoError(t, module.Provide[identity.PrincipalStateStore](services, m.principals))
	require.NoError(t, module.Provide[identity.ServiceAccountValidator](services, m.accounts))
	require.NoError(t, module.Provide[audit.Appender](services, m.audit))
	require.NoError(t, eventModule.ProvideServices(services))
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	require.NoError(t, eventModule.InjectServices(services))
	require.NoError(t, m.InjectServices(services))
	require.NoError(t, eventModule.Initialize(ctx))
	require.NoError(t, m.Initialize(ctx))
	directory, err := module.Resolve[MachineDirectory](services)
	require.NoError(t, err)
	require.NotNil(t, directory.ByIDOn)
	require.NotNil(t, directory.ResolveWorkspaceOn)
	require.NotNil(t, directory.ReserveSlotOn)
	require.NotNil(t, directory.ReleaseSlotOn)
	require.NotNil(t, directory.LockOn)
	require.NotNil(t, directory.ValidateSessionOn)
	require.NotNil(t, directory.ReacquireSlotOn)
	require.Equal(t, []string{files.Name, guid.Name, identity.Name, audit.Name, events.Name, core.Name, jobs.Name, platformi18n.Name, IAMBridgeName, dlock.Name, idempotency.Name, authenticationModule}, m.ModuleDependencies())
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		session, err := m.OpenSessionOn(ctx, tx, 7, 1, "real-outbox")
		require.NoError(t, err)
		fact, err := directory.LockOn(ctx, tx, 7, 1)
		require.NoError(t, err)
		require.Equal(t, guid.ID(1), fact.ID)
		require.Equal(t, "ONLINE", fact.State)
		_, err = directory.LockOn(ctx, tx, 8, 1)
		require.ErrorIs(t, err, ErrMachineNotFound)
		require.ErrorIs(t, directory.ValidateSessionOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch+1), ErrSessionStale)
		require.NoError(t, directory.ValidateSessionOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch))
		_, err = directory.ReserveSlotOn(ctx, tx, 7, 1, 900)
		require.NoError(t, err)
		require.NoError(t, directory.ReleaseSlotOn(ctx, tx, 7, 1, 900))
		_, err = directory.ReacquireSlotOn(ctx, tx, 7, 1, 900)
		return err
	}))
	_, err = directory.LockOn(ctx, db, 7, 1)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.machineEventOn(humanMachineContext(ctx), tx, 7, 1, "machine.runtimes_changed", 1, map[string]any{"runtime": "CODEX", "catalog_version": 1})
	}), "manual catalog changes must publish through the real registered event catalog")
}

func TestMachineModuleMissingDependenciesFailClosed(t *testing.T) {
	for missing := -1; missing < 16; missing++ {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			m, _, _, _ := machineHarness(t)
			registry, err := authz.NewRegistry()
			require.NoError(t, err)
			eventModule := events.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")))
			fm := files.NewModule(m.runtime, files.Config{LocalRoot: t.TempDir()})
			providers := []func(*module.Services) error{
				func(s *module.Services) error {
					return module.Provide[dlock.RenewingLease](s, func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
						return nil, ErrUnavailable
					})
				},
				func(s *module.Services) error { return module.Provide[EnrollmentIAM](s, (&enrollmentIAMFake{}).port()) },
				func(s *module.Services) error {
					return module.Provide[core.TenantConstraintOn](s, func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
						return authz.DataConstraint{AllowAll: true}, nil
					})
				},
				func(s *module.Services) error {
					return module.Provide[*authz.Registrar](s, authz.NewUnboundRegistrar(registry))
				},
				func(s *module.Services) error { return module.Provide[guid.Source](s, m.ids) },
				func(s *module.Services) error { return module.Provide[identity.PrincipalStateStore](s, m.principals) },
				func(s *module.Services) error { return module.Provide[identity.ServiceAccountValidator](s, m.accounts) },
				func(s *module.Services) error { return module.Provide[audit.Appender](s, m.audit) },
				func(s *module.Services) error { return module.Provide[events.Publisher](s, m.events) },
				func(s *module.Services) error { return module.Provide[events.Catalog](s, eventModule) },
				fm.ProvideServices,
				func(s *module.Services) error {
					return module.Provide[core.AuthorizationExplanation](s, func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
						return authz.Explanation{Allowed: true}, nil
					})
				},
				func(s *module.Services) error {
					return module.Provide[authn.ReauthenticationConsumer](s, func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil })
				},
				func(s *module.Services) error {
					return module.Provide[identity.ServiceAccountDisabler](s, func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil })
				},
				func(s *module.Services) error {
					return module.Provide[authn.BrowserSessionSecurity](s, authn.BrowserSessionSecurity{})
				},
				func(s *module.Services) error {
					return module.Provide[idempotency.Store](s, idempotency.NewModule(m.runtime))
				},
			}
			services := module.NewServices()
			for i, provide := range providers {
				if i != missing {
					require.NoError(t, provide(services))
				}
			}
			require.NoError(t, services.Seal())
			err = m.InjectServices(services)
			if missing < 0 {
				require.NoError(t, err, "complete capability configuration must wire")
			} else {
				require.ErrorIs(t, err, module.ErrServiceMissing, "each omitted dependency must be reached")
			}
		})
	}
	require.ErrorIs(t, (&Module{}).Initialize(context.Background()), ErrUnavailable)
}

func TestMachineModuleRejectsConflictingCapabilityProviders(t *testing.T) {
	m, _, _, _ := machineHarness(t)
	for _, provide := range []func(*module.Services) error{
		func(s *module.Services) error { return module.Provide[RepositoryConnector](s, RepositoryConnector{}) },
		func(s *module.Services) error { return module.Provide[AgentDirectory](s, AgentDirectory{}) },
		func(s *module.Services) error { return module.Provide[MachineDirectory](s, MachineDirectory{}) },
	} {
		s := module.NewServices()
		require.NoError(t, provide(s))
		require.Error(t, m.ProvideServices(s))
	}
}

func TestMachineCompositionRejectsEveryCompetingCapabilityOwner(t *testing.T) {
	m, _, _, _ := machineHarness(t)
	providers := []func(*module.Services) error{
		func(s *module.Services) error { return module.Provide(s, MachineExecutionHistoryConnector{}) },
		func(s *module.Services) error { return module.Provide(s, WorkdirLifecycleConnector{}) },
		func(s *module.Services) error { return module.Provide(s, VerifierAgents{}) },
		func(s *module.Services) error { return module.Provide(s, VerifierTools{}) },
		func(s *module.Services) error { return module.Provide[ProjectArchiveFacts](s, m.projectArchiveFactsOn) },
		func(s *module.Services) error { return module.Provide(s, AgentRetirementObserverConnector{}) },
		func(s *module.Services) error { return module.Provide(s, AgentRetirementRevocationConnector{}) },
		func(s *module.Services) error { return module.Provide(s, AgentRetirementRuntime{}) },
		func(s *module.Services) error { return module.Provide(s, AgentProvisioner{}) },
		func(s *module.Services) error { return module.Provide(s, AgentProfiles{}) },
		func(s *module.Services) error { return module.Provide(s, AgentCatalogPolicy{}) },
		func(s *module.Services) error { return module.Provide(s, AgentWorkspaceMachine{}) },
		func(s *module.Services) error { return module.Provide(s, ToolSourceFacts{}) },
		func(s *module.Services) error { return module.Provide(s, TerminalExecutionConnector{}) },
		func(s *module.Services) error { return module.Provide(s, PreviewAccessResolverConnector{}) },
		func(s *module.Services) error { return module.Provide[*PreviewRuntime](s, &PreviewRuntime{}) },
		func(s *module.Services) error { return module.Provide(s, GatewaySettlementConnector{}) },
		func(s *module.Services) error { return module.Provide(s, AgentSpawnConnector{}) },
		func(s *module.Services) error { return module.Provide(s, AgentRebindConnector{}) },
		func(s *module.Services) error {
			return module.Provide[MachineOfflineMachines](s, m.machineOfflineMachinesOn)
		},
		func(s *module.Services) error { return module.Provide[MachineNoticeFacts](s, m.machineNoticeFactsOn) },
		func(s *module.Services) error {
			return module.Provide[EnrollmentNotificationFacts](s, m.enrollmentNotificationFactsOn)
		},
		func(s *module.Services) error { return module.Provide[WorkbenchCapacity](s, m.workbenchCapacityOn) },
		func(s *module.Services) error {
			return module.Provide[GatewayRotationFacts](s, m.gatewayRotationFactOn)
		},
		func(s *module.Services) error { return module.Provide(s, AgentRegistrationCommands{}) },
		func(s *module.Services) error { return module.Provide[WorkbenchMachines](s, m.workbenchMachinesOn) },
		func(s *module.Services) error { return module.Provide(s, AgentRegistrationRolesConnector{}) },
		func(s *module.Services) error { return module.Provide(s, EnsembleConnector{}) },
		func(s *module.Services) error { return module.Provide(s, SpawnGrantDirectory{}) },
		func(s *module.Services) error { return module.Provide(s, DispatchFleetFacts{}) },
		func(s *module.Services) error { return module.Provide(s, RealtimeTopics{}) },
		func(s *module.Services) error {
			return module.Provide[RotationNotificationFacts](s, m.rotationNotificationFacts)
		},
		func(s *module.Services) error {
			return module.Provide[RotationNotificationRequester](s, m.rotationNotificationRequester)
		},
		func(s *module.Services) error { return module.Provide(s, GatewayBudgetConnector{}) },
		func(s *module.Services) error { return module.Provide(s, AgentRuntimeFacts{}) },
	}
	for i, provide := range providers {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			m, _, _, _ := machineHarness(t)
			services := module.NewServices()
			require.NoError(t, provide(services))
			require.ErrorIs(t, m.ProvideServices(services), module.ErrServiceDuplicate)
		})
	}
}
