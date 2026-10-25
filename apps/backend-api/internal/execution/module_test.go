package execution

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/templates"
	"7link.com/platform/internal/yunxiao"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type executionCatalogProbe struct {
	owner string
	specs []events.TypeSpec
}

func TestExecutionModuleLocksPrincipalsBeforeProjectAdapters(t *testing.T) {
	for _, operation := range []string{"create", "cancel", "pull", "receipt"} {
		t.Run(operation, func(t *testing.T) {
			f := executionHarness(t)
			var id guid.ID
			if operation != "create" {
				id = f.create(t).ID
				if operation == "receipt" {
					f.pull(t, id)
				}
			}
			seen := map[guid.ID]bool{}
			var order []guid.ID
			businessStarted := false
			f.m.principals.Lock = func(_ context.Context, _ *gorm.DB, principal guid.ID) error {
				if !seen[principal] {
					if businessStarted {
						return fmt.Errorf("principal %s first locked after project/assignment", principal)
					}
					seen[principal] = true
					order = append(order, principal)
				}
				return nil
			}
			originalPolicy := f.m.policy
			f.m.policy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
				businessStarted = true
				return originalPolicy(ctx, tx, tenant, project, permission, actor)
			}
			originalAuthorize := f.m.work.AuthorizeOn
			f.m.work.AuthorizeOn = func(ctx context.Context, tx *gorm.DB, l WorkLink, action string) error {
				require.Equal(t, guid.ID(11), l.InitiatorID, "delivery must receive the persisted initiator to revalidate")
				require.Equal(t, authn.PrincipalIDFromContext(ctx), l.ActorID)
				require.True(t, seen[11], "initiator must be locked before the delivery adapter")
				require.True(t, seen[101], "machine principal must be locked before the delivery adapter")
				businessStarted = true
				return originalAuthorize(ctx, tx, l, action)
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				switch operation {
				case "create":
					_, err := f.m.CreateOn(f.human, tx, 7, validCreate())
					return err
				case "cancel":
					_, err := f.m.CancelOn(f.human, tx, 7, id, 1)
					return err
				case "pull":
					_, err := f.m.PullOn(f.runner, tx, 7, f.fence)
					return err
				default:
					_, err := f.m.ReceiptOn(f.runner, tx, 7, id, f.fence, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
					return err
				}
			})
			require.NoError(t, err)
			require.Equal(t, []guid.ID{11, 101}, order, "new principal locks must be taken in ascending order")
		})
	}
}

func (p *executionCatalogProbe) AddTypes(owner string, specs ...events.TypeSpec) error {
	p.owner, p.specs = owner, specs
	return nil
}

func TestExecutionModuleWiringAndMissingDependencies(t *testing.T) {
	f := executionHarness(t)
	require.NoError(t, platformi18n.NewModule(nil, "en-US").Initialize(t.Context()))
	require.Equal(t, Name, f.m.ModuleName())
	require.ElementsMatch(t, []string{idempotency.Name, templates.Name, delivery.Name, iamauthn.Name, knowledge.Name, agents.Name, yunxiao.Name, fleet.Name, files.Name, identity.Name, core.Name, guid.Name, audit.Name, events.Name, DeliveryBridgeName, jobs.Name, platformi18n.Name}, f.m.ModuleDependencies())
	require.False(t, f.m.Enabled())
	f.m.config.Enabled = true
	require.True(t, f.m.Enabled())
	require.False(t, (*Module)(nil).Enabled())

	for omitted := -1; omitted < 26; omitted++ {
		name := "complete"
		if omitted >= 0 {
			name = fmt.Sprintf("missing_dependency_%d", omitted)
		}
		t.Run(name, func(t *testing.T) {
			services, catalog := executionModuleServices(t, f, omitted)
			require.NoError(t, services.Seal())
			m := &Module{}
			err := m.InjectServices(services)
			if omitted >= 0 {
				require.ErrorIs(t, err, module.ErrServiceMissing)
				return
			}
			require.NoError(t, err)
			require.NoError(t, m.Initialize(context.Background()))
			require.Equal(t, Name, catalog.owner)
			require.ElementsMatch(t, []events.TypeSpec{
				{Name: "execution.merge_requested", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.merge_finished", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.merged", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},

				{Name: "execution.branch_created", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.verifier_changed", SchemaVersion: 1, SubjectKind: "verifier", Realtime: events.RealtimeSignal},
				{Name: "execution.preview_changed", SchemaVersion: 1, SubjectKind: "preview_session", Realtime: events.RealtimeSignal},
				{Name: "execution.artifact_comment_added", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.comment_thread_resolved", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.comment_thread_reopened", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.comment_edited", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.comment_retracted", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.comment_anchor_checked", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.comment_converted", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.full_auto_enabled", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.state_changed", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.evidence_recorded", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.verification_queued", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
				{Name: "execution.verification_completed", SchemaVersion: 1, SubjectKind: "execution", Realtime: events.RealtimeSignal},
			}, catalog.specs)
			t.Run("TestCodeMergeRealOutboxCatalog", func(t *testing.T) {
				testCodeMergeRealOutboxCatalog(t, catalog.specs)
			})
		})
	}
}

func TestExecutionModuleInitializationFailsClosed(t *testing.T) {
	f := executionHarness(t)
	require.NoError(t, platformi18n.NewModule(nil, "en-US").Initialize(t.Context()))
	for _, missing := range []struct {
		name  string
		clear func(*Module)
	}{
		{"ids", func(m *Module) { m.ids = nil }},
		{"policy", func(m *Module) { m.policy = nil }},
		{"principal_lock", func(m *Module) { m.principals.Lock = nil }},
		{"audit", func(m *Module) { m.audit = nil }},
		{"events", func(m *Module) { m.events = nil }},
		{"work_authorize", func(m *Module) { m.work.AuthorizeOn = nil }},
		{"work_started", func(m *Module) { m.work.StartedOn = nil }},
		{"work_finished", func(m *Module) { m.work.FinishedOn = nil }},
		{"work_budget", func(m *Module) { m.work.BudgetOn = nil }},
		{"work_reserve_budget", func(m *Module) { m.work.ReserveBudgetOn = nil }},
	} {
		t.Run(missing.name, func(t *testing.T) {
			require.NoError(t, platformi18n.NewModule(nil, "en-US").Initialize(t.Context()))
			m := *f.m
			missing.clear(&m)
			require.ErrorIs(t, m.Initialize(t.Context()), ErrUnavailable)
		})
	}
}

func executionModuleServices(t *testing.T, f *executionFixture, omitted int) (*module.Services, *executionCatalogProbe) {
	t.Helper()
	services := module.NewServices()
	catalog := &executionCatalogProbe{}
	providers := []func() error{
		func() error { return module.Provide[guid.Source](services, f.m.ids) },
		func() error { return module.Provide[fleet.MachineDirectory](services, f.m.machines) },
		func() error { return module.Provide[identity.PrincipalStateStore](services, f.m.principals) },
		func() error { return module.Provide[core.EntityConstraintOn](services, f.m.policy) },
		func() error {
			registry, err := authz.NewRegistry()
			if err != nil {
				return err
			}
			return module.Provide[*authz.Registrar](services, authz.NewUnboundRegistrar(registry))
		},
		func() error { return module.Provide[audit.Appender](services, f.m.audit) },
		func() error { return module.Provide[events.Publisher](services, f.m.events) },
		func() error { return module.Provide[WorkItems](services, f.m.work) },
		func() error { return module.Provide[events.Catalog](services, catalog) },
		func() error {
			fm := files.NewModule(f.m.runtime, files.Config{LocalRoot: t.TempDir()})
			return fm.ProvideServices(services)
		},
		func() error { return module.Provide[*fleet.PreviewRuntime](services, &fleet.PreviewRuntime{}) },
		func() error {
			return module.Provide[fleet.PreviewAccessResolverConnector](services, fleet.PreviewAccessResolverConnector{Bind: func(fleet.PreviewAccessResolver) error { return nil }})
		},
		func() error {
			return module.Provide[fleet.TerminalExecutionConnector](services, fleet.TerminalExecutionConnector{Bind: func(fleet.TerminalExecutionResolver) error { return nil }})
		},
		func() error { return idempotency.NewModule(f.m.runtime).ProvideServices(services) },
		func() error {
			return module.Provide[core.AuthorizationExplanation](services, func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{}, ErrUnavailable
			})
		},
		func() error {
			return module.Provide[delivery.VerifierWorkPolicy](services, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, string, error) {
				return "", "", ErrCapabilityUnavailable
			})
		},
		func() error {
			return module.Provide[core.ActiveMemberChecker](services, commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }))
		},
		func() error {
			return module.Provide[delivery.WorkItemActivityRecorder](services, delivery.WorkItemActivityRecorder{})
		},
		func() error {
			return module.Provide[delivery.WorkItemActivityReader](services, delivery.WorkItemActivityReader{})
		},
		func() error {
			return module.Provide[delivery.ArtifactCommentsConnector](services, delivery.ArtifactCommentsConnector{Bind: func(source delivery.ArtifactComments) error {
				require.NotNil(t, source.ScopeOn)
				require.NotNil(t, source.GetOn)
				require.NotNil(t, source.MarkConvertedOn)
				return nil
			}})
		},
		func() error { return module.Provide[templates.VerifierCatalog](services, templates.VerifierCatalog{}) },
		func() error { return module.Provide[delivery.RealtimeTopics](services, delivery.RealtimeTopics{}) },
		func() error {
			return module.Provide[authn.ReauthenticationConsumer](services, func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil })
		},
		func() error { return module.Provide[fleet.AgentDirectory](services, f.m.agents) },
		func() error { return module.Provide[fleet.AgentRuntimeFacts](services, f.m.agentRuntime) },
		func() error { return module.Provide[governance.InboxSources](services, inboxCapture{}) },
	}
	for i, provide := range providers {
		if i != omitted {
			require.NoError(t, provide())
		}
	}
	return services, catalog
}

func TestExecutionWiringPropagatesFailedOwnerBindings(t *testing.T) {
	failure := errors.New("owner refused execution capability binding")
	for _, tc := range []struct {
		name    string
		provide func(*module.Services) error
	}{
		{"machine_history", func(s *module.Services) error {
			return module.Provide(s, fleet.MachineExecutionHistoryConnector{Bind: func(fleet.MachineExecutionHistoryReader) error { return failure }})
		}},
		{"attention", func(s *module.Services) error {
			return module.Provide(s, delivery.AttentionInteractionConnector{Bind: func(delivery.AttentionInteraction) error { return failure }})
		}},
		{"metrics", func(s *module.Services) error {
			return module.Provide(s, delivery.DeliveryMetricsSourcesConnector{BindExecutions: func(func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]delivery.MetricExecution, error)) error {
				return failure
			}})
		}},
		{"defect_merge", func(s *module.Services) error {
			return module.Provide(s, delivery.DefectMergeConnector{Bind: func(delivery.DefectMergeReader) error { return failure }})
		}},
		{"performance", func(s *module.Services) error {
			return module.Provide(s, agents.AgentPerformanceConnector{BindRunning: func(func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error)) error { return failure }})
		}},
		{"workdir", func(s *module.Services) error {
			return module.Provide(s, fleet.WorkdirLifecycleConnector{Bind: func(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)) error { return failure }})
		}},
		{"project_stage", func(s *module.Services) error {
			return module.Provide(s, delivery.ProjectStageExecutionsConnector{Bind: func(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)) error { return failure }})
		}},
		{"archive", func(s *module.Services) error {
			return module.Provide(s, delivery.ProjectArchiveConnector{Bind: func(string, delivery.ProjectArchiveReader) error {
				return failure
			}})
		}},
		{"agent_rebind", func(s *module.Services) error {
			return module.Provide(s, fleet.AgentRebindConnector{Bind: func(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)) error { return failure }})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := executionHarness(t)
			s, _ := executionModuleServices(t, f, -1)
			require.NoError(t, tc.provide(s))
			require.NoError(t, s.Seal())
			m := &Module{}
			require.ErrorIs(t, m.InjectServices(s), failure)
		})
	}
}
