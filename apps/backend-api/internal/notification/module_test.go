package notification

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/infra/realtime"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type testIDs struct {
	next atomic.Int64
	err  error
	zero bool
}

func (s *testIDs) Next() (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	if s.zero {
		return 0, nil
	}
	return s.next.Add(1), nil
}

type memberChecker struct {
	active bool
	err    error
}

func (s *memberChecker) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return s.active, s.err
}
func (s *memberChecker) IsMemberActiveOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
	return s.active, s.err
}

type fixturePublisher struct{}

func (*fixturePublisher) Publish(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	data, err := json.Marshal(e.Data)
	if err != nil {
		return 0, err
	}
	return 100, tx.Exec("INSERT INTO test_notification_events(type,data) VALUES (?,?)", e.Type, string(data)).Error
}

type fixtureQueue struct {
	requests []jobs.EnqueueRequest
	next     atomic.Int64
	err      error
}

func (q *fixtureQueue) Enqueue(_ context.Context, tx *gorm.DB, r jobs.EnqueueRequest) (jobs.EnqueueResult, error) {
	if q.err != nil {
		return jobs.EnqueueResult{}, q.err
	}
	q.requests = append(q.requests, r)
	id := guid.ID(q.next.Add(1) + 10000)
	data, err := json.Marshal(r.Payload)
	if err != nil {
		return jobs.EnqueueResult{}, err
	}
	err = tx.Exec("INSERT INTO test_notification_jobs(id,data) VALUES (?,?)", id, string(data)).Error
	return jobs.EnqueueResult{JobID: id, Created: true}, err
}
func (q *fixtureQueue) EnqueueBatch(ctx context.Context, tx *gorm.DB, rs []jobs.EnqueueRequest) ([]jobs.EnqueueResult, error) {
	out := []jobs.EnqueueResult{}
	for _, r := range rs {
		v, err := q.Enqueue(ctx, tx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (q *fixtureQueue) Cancel(context.Context, *gorm.DB, guid.ID, string, string) (bool, error) {
	return false, nil
}

type fixtureCatalog struct {
	subscribers *[]events.Subscriber
	err         error
}

func (fixtureCatalog) AddTypes(string, ...events.TypeSpec) error { return nil }
func (c fixtureCatalog) AddSubscribers(_ string, subscribers ...events.Subscriber) error {
	if c.err != nil {
		return c.err
	}
	if c.subscribers != nil {
		*c.subscribers = append(*c.subscribers, subscribers...)
	}
	return nil
}

type fixture struct {
	m       *Module
	db      *gorm.DB
	members *memberChecker
	human   bool
	allow   bool
	err     error
	ids     *testIDs
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"main": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "notification.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix("n_"))
	m := NewModule(rt, Config{Config: module.Config{Enabled: true}})
	m.machineOfflineMachines = func(context.Context, *gorm.DB, guid.ID, guid.ID, int) ([]guid.ID, error) { return []guid.ID{}, nil }
	m.machineNoticeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.MachineNoticeFact, bool, error) {
		return fleet.MachineNoticeFact{}, false, nil
	}
	m.collaborationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (clientwork.CollaborationNotificationFact, bool, error) {
		return clientwork.CollaborationNotificationFact{}, false, nil
	}
	m.subjectHumanActor = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID, string) (guid.ID, bool, error) {
		return 0, false, nil
	}
	m.exceptionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (clientwork.ExceptionNotificationFact, bool, error) {
		return clientwork.ExceptionNotificationFact{}, false, nil
	}
	m.delegationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (governance.DelegationNotificationFact, bool, error) {
		return governance.DelegationNotificationFact{}, false, nil
	}
	raw, err := router.Write().DB()
	require.NoError(t, err)
	source := m.MigrationSource()
	source.Prefix = "n_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	f := &fixture{m: m, db: router.Write(), members: &memberChecker{active: true}, human: true, allow: true, ids: &testIDs{}}
	m.ids = f.ids
	m.ruleEligible = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return f.allow, f.err }
	m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
		return execution.NotificationFacts{ProjectID: 81, WorkItemID: 91, InitiatorID: 11, MachineID: 92, State: "COST_PAUSED"}, nil
	}
	m.roles = governance.SeededRoles{RoleIDOn: func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) { return 10, nil }}
	m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: f.allow, Matches: []authz.DecisionMatch{{Effect: "allow", RoleID: "10", SourceType: "entity_binding", ScopeID: r.EntityID.String()}}}, f.err
	}
	m.workFacts = delivery.WorkNotifications{FactsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.WorkNotificationFacts, bool, error) {
		return delivery.WorkNotificationFacts{ProjectID: 81, ExecutorID: 11, AssignerID: 12}, true, f.err
	}, ReviewEligibleOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return f.allow, f.err }}
	m.workFacts.RequirementExecutorOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, nil }
	m.workFacts.ActiveEngineerOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, nil }
	m.workFacts.ProjectModeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error) { return "HYBRID", nil }
	m.activity = agents.ActivityNotifications{Bind: func(agents.ActivityObserver) error { return nil }, SnapshotOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (agents.ActivitySnapshot, bool, error) {
		return agents.ActivitySnapshot{}, false, nil
	}}
	require.NoError(t, f.db.Exec("CREATE TABLE test_notification_events(type TEXT, data TEXT)").Error)
	require.NoError(t, f.db.Exec("CREATE TABLE test_notification_jobs(id BIGINT PRIMARY KEY,data TEXT)").Error)
	m.publisher = &fixturePublisher{}
	m.queue = &fixtureQueue{}
	m.admission = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	m.guard = func(context.Context, *gorm.DB, string) error { return nil }
	m.members = f.members
	m.broadcast = func(context.Context, string, any) error { return nil }
	m.projectAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return f.allow, f.err }
	m.principal = identity.PrincipalStateStore{LockActive: func(context.Context, *gorm.DB, guid.ID) error { return f.err }, ActiveHumanByID: func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, f.human, f.err
	}}
	m.platformPermission = func(context.Context, *gorm.DB, string, guid.ID) (bool, error) { return f.allow, f.err }
	m.tenantPermission = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: f.allow}, f.err
	}
	m.entityPermission = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: f.allow}, f.err
	}
	m.candidates = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]guid.ID, error) {
		if after == 0 {
			return []guid.ID{11, 12}, nil
		}
		return nil, nil
	}
	m.rotationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.RotationNotificationFact, bool, error) {
		return fleet.RotationNotificationFact{}, false, nil
	}
	m.rotationRequester = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error) { return 11, nil }
	m.now = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
	m.registrar = authz.NewDeclarationOnlyRegistrar(nilRegistry(t))
	require.NoError(t, i18n.NewModule(nil, "en-US").Initialize(t.Context()))
	require.NoError(t, m.Initialize(t.Context()))
	return f
}
func nilRegistry(t *testing.T) *authz.Registry {
	r, err := authz.NewRegistry()
	require.NoError(t, err)
	return r
}
func sample() Notification {
	return Notification{TenantID: 7, ProjectID: 9, Recipients: []guid.ID{11}, Type: "machine.offline", Severity: Action, TemplateKey: "notification.event", Params: map[string]string{"subject_id": "91", "type": "machine.offline"}, Permission: "machine_manage", DedupeKey: "machine:91"}
}
func (f *fixture) notify(t *testing.T, n Notification) []guid.ID {
	t.Helper()
	var out []guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var err error; out, err = f.m.Notify(t.Context(), tx, n); return err }))
	return out
}
func TestModuleLifecycleWiring(t *testing.T) {
	f := newFixture(t)
	m := f.m
	require.Equal(t, Name, m.ModuleName())
	require.True(t, m.Enabled())
	require.False(t, (*Module)(nil).Enabled())
	require.Contains(t, m.ModuleDependencies(), fleet.Name)
	require.Len(t, m.JobHandlers(), 11)
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.Error(t, m.ProvideServices(services))
	missing := module.NewServices()
	require.NoError(t, missing.Seal())
	require.Error(t, NewModule(m.runtime, m.config).InjectServices(missing))
	require.Error(t, (&Module{}).Initialize(t.Context()))
	_, err := (&Module{}).writer()
	require.ErrorIs(t, err, ErrUnavailable)
	providers := []func(*module.Services) error{
		func(s *module.Services) error {
			return module.Provide[fleet.MachineOfflineMachines](s, m.machineOfflineMachines)
		},
		func(s *module.Services) error {
			return module.Provide[fleet.MachineNoticeFacts](s, m.machineNoticeFacts)
		},
		func(s *module.Services) error {
			return module.Provide[clientwork.CollaborationNotificationFacts](s, m.collaborationFacts)
		},
		func(s *module.Services) error {
			return module.Provide[events.SubjectHumanActor](s, m.subjectHumanActor)
		},
		func(s *module.Services) error {
			return module.Provide[delivery.DecisionNotifications](s, m.decisionFacts)
		},
		func(s *module.Services) error {
			return module.Provide[governance.DelegationNotificationFacts](s, m.delegationFacts)
		},
		func(s *module.Services) error {
			return module.Provide[knowledge.RuleNotificationEligible](s, m.ruleEligible)
		},
		func(s *module.Services) error {
			return module.Provide[execution.NotificationFactsOn](s, m.executionFacts)
		},
		func(s *module.Services) error { return module.Provide[governance.SeededRoles](s, m.roles) },
		func(s *module.Services) error { return module.Provide[core.AuthorizationExplanation](s, m.explain) },
		func(s *module.Services) error { return module.Provide[delivery.WorkNotifications](s, m.workFacts) },
		func(s *module.Services) error { return module.Provide[agents.ActivityNotifications](s, m.activity) },
		func(s *module.Services) error { return module.Provide[collab.MentionNotifications](s, m.mentions) },
		func(s *module.Services) error { return module.Provide[realtime.Broadcaster](s, m.broadcast) },
		func(s *module.Services) error {
			return module.Provide[delivery.ProjectNotificationAuthority](s, m.projectAuthority)
		},
		func(s *module.Services) error { return module.Provide[jobs.QueueAdmission](s, m.admission) },
		func(s *module.Services) error { return module.Provide[dlock.TransactionGuard](s, m.guard) },
		func(s *module.Services) error { return module.Provide[events.Publisher](s, m.publisher) },
		func(s *module.Services) error { return module.Provide[jobs.Queue](s, m.queue) },
		func(s *module.Services) error { return module.Provide[guid.Source](s, m.ids) }, func(s *module.Services) error { return module.Provide[*authz.Registrar](s, m.registrar) }, func(s *module.Services) error { return module.Provide[identity.PrincipalStateStore](s, m.principal) }, func(s *module.Services) error { return module.Provide[core.ActiveMemberChecker](s, m.members) }, func(s *module.Services) error {
			return module.Provide[core.PlatformPermissionOn](s, m.platformPermission)
		}, func(s *module.Services) error { return module.Provide[core.TenantConstraintOn](s, m.tenantPermission) }, func(s *module.Services) error { return module.Provide[core.EntityConstraintOn](s, m.entityPermission) }, func(s *module.Services) error {
			return module.Provide[identity.NotificationCandidates](s, m.candidates)
		}, func(s *module.Services) error {
			return module.Provide[fleet.RotationNotificationFacts](s, m.rotationFacts)
		}, func(s *module.Services) error {
			return module.Provide[fleet.RotationNotificationRequester](s, m.rotationRequester)
		}}
	providers = append(providers, func(s *module.Services) error {
		return module.Provide[clientwork.ExceptionNotificationFacts](s, m.exceptionFacts)
	})
	providers = append(providers, func(s *module.Services) error { return module.Provide[events.Catalog](s, fixtureCatalog{}) }, func(s *module.Services) error { return module.Provide[events.SubscriberCatalog](s, fixtureCatalog{}) })
	providers = append(providers, func(s *module.Services) error {
		return module.Provide[events.ParkedNotifications](s, events.ParkedNotifications{Bind: func(func(context.Context, *gorm.DB, events.ParkedNotice) error) error { return nil }})
	})
	for count := 0; count <= len(providers); count++ {
		s := module.NewServices()
		for _, provide := range providers[:count] {
			require.NoError(t, provide(s))
		}
		require.NoError(t, s.Seal())
		target := NewModule(m.runtime, m.config)
		err := target.InjectServices(s)
		if count < len(providers) {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
	q := query.BindNotifications(f.db, m.names)
	_, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	f.err = errors.New("fault")
}
