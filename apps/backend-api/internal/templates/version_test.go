package templates

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"path/filepath"
	"testing"
)

type testPublisher func(context.Context, *gorm.DB, events.Event) (guid.ID, error)

func (f testPublisher) Publish(c context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	return f(c, tx, e)
}
func templateHarness(t *testing.T) (*Module, *gorm.DB, context.Context) {
	t.Helper()
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	router, e := gormx.NewDatasourceRouter(ctx, false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "templates.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, e)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("t_"), appenv.WithDatabase(router))
	m := NewModule(rt, Config{Config: module.Config{Enabled: true}})
	db := router.Write()
	raw, e := db.DB()
	require.NoError(t, e)
	source := m.MigrationSource()
	source.Prefix = "t_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	ids, e := guid.New(1)
	require.NoError(t, e)
	m.ids = ids
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.principals = identity.PrincipalStateStore{Lock: func(context.Context, *gorm.DB, guid.ID) error { return nil }, ActiveHumanByID: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}}
	m.scope = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }}
	m.teamWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.TeamWorkFact, error) {
		return delivery.TeamWorkFact{}, nil
	}
	m.teamHuman = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }
	m.delivery = delivery.TemplateInstantiation{CreateWorkOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TemplateWorkInput) (guid.ID, error) {
		return 700, nil
	}, InitializeConstitutionOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (guid.ID, error) { return 800, nil }, WorkStatusOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return "DRAFT", nil }}
	m.audit = func(_ context.Context, _ *gorm.DB, event audit.DomainEvent) error {
		require.NotEmpty(t, event.Detail["authority_source"])
		return nil
	}
	m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 1, nil })
	store := idempotency.NewModule(rt)
	source = store.MigrationSource()
	source.Prefix = "t_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	s := module.NewServices()
	locks := dlock.NewModule(rt)
	require.NoError(t, locks.ProvideServices(s))
	source = locks.MigrationSource()
	source.Prefix = "t_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	require.NoError(t, module.Provide[guid.Source](s, ids))
	require.NoError(t, s.Seal())
	require.NoError(t, store.InjectServices(s))
	m.idem = store
	return m, db, ctx
}
func draftTemplate(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, key string) TemplateVersionView {
	t.Helper()
	var v TemplateVersionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.draftOn(ctx, tx, key, testDefinition()); return e }))
	return v
}
func TestVersionsPublishedHashAndIsolation(t *testing.T) {
	m, db, ctx := templateHarness(t)
	v := draftTemplate(t, m, db, ctx, "software")
	require.Equal(t, "DRAFT", v.State)
	newNode := v.Definition.Workflow.Nodes[0]
	newNode.Name = "Verified build"
	patch := TemplateWorkflowPatch{BaseVersion: 1, BaseHash: v.Hash, Reason: "Rename", Operations: []TemplatePatchOperation{{Kind: "update_node", ID: "work", Node: &newNode}}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.patchOn(ctx, tx, v.ID, patch); return e }))
	require.Equal(t, 2, v.Version)
	hash := v.Hash
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.publishOn(ctx, tx, v.ID, hash); return e }))
	require.Equal(t, "PUBLISHED", v.State)
	require.Equal(t, hash, v.Hash)
	patch.BaseHash = v.Hash
	patch.BaseVersion = v.Definition.Workflow.Version
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.patchOn(ctx, tx, v.ID, patch); return e }), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.publishOn(ctx, tx, v.ID, hash); return e }), ErrConflict)
	other := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.versionOn(other, tx, v.ID, "template_read"); return e }), ErrNotFound)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		children, e := m.childrenOn(ctx, tx)
		require.NoError(t, e)
		require.Equal(t, v.Definition.Workflow, children[TemplateWorkflowRef{"software", 1}])
		return nil
	}))
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.versionOn(ctx, tx, v.ID, "template_read"); return e }), ErrForbidden)
}
func TestVersionFailuresRollback(t *testing.T) {
	for _, stage := range []string{"policy", "lock", "human", "inactive", "audit", "event", "ids", "key", "invalid", "create"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			injected := errors.New("injected")
			d := testDefinition()
			key := "software"
			switch stage {
			case "policy":
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, injected
				}
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, injected
				}
			case "inactive":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return injected }
			case "event":
				m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, injected })
			case "ids":
				m.ids = failingIDs{}
			case "key":
				key = " "
			case "invalid":
				d.Domain = "OTHER"
			case "create":
				require.NoError(t, db.Exec("DROP TABLE t_templates_versions").Error)
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.draftOn(ctx, tx, key, d); return e }))
			if stage != "create" {
				q := query.BindVersions(db, m.names)
				count, e := q.WithContext(ctx).Count()
				require.NoError(t, e)
				require.Zero(t, count)
			}
		})
	}
}

type failingIDs struct{}

func (failingIDs) Next() (int64, error) { return 0, errors.New("ids unavailable") }
func TestAuthorizationRequiresActiveTransactionAndCurrentHuman(t *testing.T) {
	m, db, ctx := templateHarness(t)
	require.ErrorIs(t, m.authorizeOn(ctx, nil, "template_read", 11), ErrForbidden)
	require.ErrorIs(t, m.authorizeOn(ctx, db, "template_read", 11), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.authorizeOn(context.Background(), tx, "template_read", 11) }), ErrForbidden)
	m.policy = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.authorizeOn(ctx, tx, "template_read", 11) }), ErrUnavailable)
}

func TestVersionCorruptionConflictAndSubflowScope(t *testing.T) {
	m, db, ctx := templateHarness(t)
	v := draftTemplate(t, m, db, ctx, "a")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.publishOn(ctx, tx, v.ID, "stale"); return e }), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.patchOn(ctx, tx, v.ID, TemplateWorkflowPatch{BaseVersion: 1, BaseHash: "stale", Reason: "x"})
		return e
	}), ErrConflict)
	q := query.BindVersions(db, m.names)
	_, e := q.WithContext(ctx).Where(q.ID.Eq(v.ID)).Update(q.Definition, "corrupt")
	require.NoError(t, e)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.publishOn(ctx, tx, v.ID, v.Hash); return e }))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.patchOn(ctx, tx, v.ID, TemplateWorkflowPatch{}); return e }))
	_, e = q.WithContext(ctx).Where(q.ID.Eq(v.ID)).Update(q.State, "PUBLISHED")
	require.NoError(t, e)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.childrenOn(ctx, tx); return e }))
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"22"}}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { children, e := m.childrenOn(ctx, tx); require.Empty(t, children); return e }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.versionOn(ctx, tx, v.ID, "template_read"); return e }), ErrForbidden)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.childrenOn(ctx, tx); return e }))
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, errors.New("policy unavailable")
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.childrenOn(ctx, tx); return e }))
}

func TestVersionWritesRollbackStorageAndPublicationEffects(t *testing.T) {
	for _, failure := range []string{"draft-query", "draft-create", "patch-update", "publish-update", "patch-event", "publish-event"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			v := draftTemplate(t, m, db, ctx, "version-storage")
			injected := errors.New("version persistence unavailable")
			if failure == "draft-query" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("version-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Versions.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Query().Remove("version-failure")
			}
			if failure == "draft-create" {
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("version-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Versions.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Create().Remove("version-failure")
			}
			if failure == "patch-update" || failure == "publish-update" {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("version-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Versions.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Update().Remove("version-failure")
			}
			if failure == "patch-event" || failure == "publish-event" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return injected }
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				switch failure {
				case "draft-query", "draft-create":
					d := testDefinition()
					d.Semver = "1.1.0"
					_, e := m.draftOn(ctx, tx, "version-storage", d)
					return e
				case "patch-update", "patch-event":
					node := v.Definition.Workflow.Nodes[0]
					node.Name = "Revised"
					_, e := m.patchOn(ctx, tx, v.ID, TemplateWorkflowPatch{BaseHash: v.Hash, BaseVersion: v.Definition.Workflow.Version, Reason: "Human edit", Operations: []TemplatePatchOperation{{Kind: "update_node", ID: node.ID, Node: &node}}})
					return e
				default:
					_, e := m.publishOn(ctx, tx, v.ID, v.Hash)
					return e
				}
			})
			require.ErrorIs(t, err, injected)
			if failure == "draft-query" {
				require.NoError(t, db.Callback().Query().Remove("version-failure"))
			}
			q := query.BindVersions(db, m.names)
			rows, e := q.WithContext(ctx).Find()
			require.NoError(t, e)
			require.Len(t, rows, 1)
			out, e := versionView(rows[0])
			require.NoError(t, e)
			require.Equal(t, v, out)
		})
	}
}

type failingTemplateIDs struct{ err error }

func (s failingTemplateIDs) Next() (int64, error) { return 0, s.err }
func TestVersionIDSourceFailurePersistsNothing(t *testing.T) {
	m, db, ctx := templateHarness(t)
	injected := errors.New("identifier unavailable")
	m.ids = failingTemplateIDs{injected}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.draftOn(ctx, tx, "id-failure", testDefinition()); return e }), injected)
	q := query.BindVersions(db, m.names)
	n, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, n)
}
