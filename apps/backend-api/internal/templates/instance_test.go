package templates

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestInstanceUsesRealProviderAndPinsExactHash(t *testing.T) {
	m, db, ctx := templateHarness(t)
	v := draftTemplate(t, m, db, ctx, "software")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.publishOn(ctx, tx, v.ID, v.Hash); return e }))
	calls := 0
	m.delivery.CreateWorkOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, in delivery.TemplateWorkInput) (guid.ID, error) {
		calls++
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), project)
		require.Equal(t, guid.ID(11), in.AccountablePrincipalID)
		require.Equal(t, "Run tests", in.Criteria[0].VerificationMethod)
		return 700, nil
	}
	var first TemplateInstanceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; first, e = m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	require.Equal(t, v.Hash, first.Hash)
	require.Equal(t, guid.ID(800), first.ConstitutionID)
	require.Len(t, first.Runs, 3)
	require.Equal(t, "WAITING", first.Runs[1].State)
	require.Equal(t, "WAITING_PREDECESSOR", first.Runs[1].Reason)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		replay, e := m.instantiateOn(ctx, tx, v.ID, 99, 11)
		require.Equal(t, first.ID, replay.ID)
		return e
	}))
	require.Equal(t, 1, calls)
	m.delivery.WorkStatusOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return "IN_PROGRESS", nil }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.instanceOn(ctx, tx, 99)
		require.Equal(t, "IN_PROGRESS", out.Runs[0].State)
		require.Equal(t, first.Workflow, out.Workflow)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instanceOn(ctx, tx, 98); return e }), ErrNotFound)
	q := query.BindBindings(db, m.names)
	_, e := q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).Update(q.SHA256, "tampered")
	require.NoError(t, e)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instanceOn(ctx, tx, 99); return e }), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }), ErrConflict)
}
func TestInstanceWaitsWithoutCreatingWorkForMissingSignedBrief(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Workflow.Nodes[0] = TemplateNode{ID: "work", Kind: "FAN_OUT", Name: "Expand", InputPath: "signed_brief.items"}
	var v TemplateVersionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.draftOn(ctx, tx, "dynamic", d)
		if e != nil {
			return e
		}
		_, e = m.publishOn(ctx, tx, v.ID, v.Hash)
		return e
	}))
	m.delivery.CreateWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TemplateWorkInput) (guid.ID, error) {
		t.Fatal("unsupported dynamic flow created work")
		return 0, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.instantiateOn(ctx, tx, v.ID, 99, 11)
		require.Equal(t, "MISSING_SIGNED_BRIEF_ITEMS", out.Runs[0].Reason)
		return e
	}))
}
func TestInstanceProviderFailureRollsBackBinding(t *testing.T) {
	for _, stage := range []string{"scope", "draft", "work", "constitution", "ids", "event"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			v := draftTemplate(t, m, db, ctx, "software")
			if stage != "draft" {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.publishOn(ctx, tx, v.ID, v.Hash); return e }))
			}
			injected := errors.New("injected")
			switch stage {
			case "scope":
				m.scope.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return injected }
			case "work":
				m.delivery.CreateWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TemplateWorkInput) (guid.ID, error) {
					return 0, injected
				}
			case "constitution":
				m.delivery.InitializeConstitutionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (guid.ID, error) { return 0, injected }
			case "ids":
				m.ids = failingIDs{}
			case "event":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return injected }
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
			count, e := query.BindBindings(db, m.names).WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, count)
		})
	}
}

func TestInstanceLazyDependenciesAndReadFailures(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	second := d.Workflow.Nodes[0]
	second.ID = "test"
	second.Name = "Test"
	d.Workflow.Nodes = append(d.Workflow.Nodes, second)
	d.Workflow.Edges = []TemplateEdge{{ID: "a", Source: "work", Target: "test"}, {ID: "b", Source: "test", Target: "team"}, {ID: "c", Source: "team", Target: "client"}}
	var v TemplateVersionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.draftOn(ctx, tx, "static", d)
		if e != nil {
			return e
		}
		_, e = m.publishOn(ctx, tx, v.ID, v.Hash)
		return e
	}))
	calls := 0
	m.delivery.CreateWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TemplateWorkInput) (guid.ID, error) {
		calls++
		return guid.ID(700 + calls), nil
	}
	dependencies := 0
	m.delivery.AddDependencyOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, work, predecessor guid.ID) error {
		dependencies++
		require.Equal(t, guid.ID(702), work)
		require.Equal(t, guid.ID(701), predecessor)
		return nil
	}
	state := "IN_PROGRESS"
	m.delivery.WorkStatusOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return state, nil }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.instantiateOn(ctx, tx, v.ID, 99, 11)
		require.Nil(t, out.Runs[3].WorkItemID)
		return e
	}))
	require.Equal(t, 1, calls)
	require.Zero(t, dependencies)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }))
	require.Equal(t, 1, calls)
	state = "ACCEPTED"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }))
	require.Equal(t, 2, calls)
	require.Equal(t, 1, dependencies)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.advanceOn(ctx, tx, 99)
		require.Equal(t, "MISSING_GATE_DECISION", out.Runs[1].Reason)
		return e
	}))
	require.Equal(t, 2, calls)
	m.delivery.WorkStatusOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) {
		return "", errors.New("facts unavailable")
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instanceOn(ctx, tx, 99); return e }))
	_, e := m.instanceHTTP(ctx, &instanceInput{ProjectID: "99"})
	assertStatus(t, e, 500)
	q := query.BindBindings(db, m.names)
	_, e = q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).Update(q.Runs, "corrupt")
	require.NoError(t, e)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instanceOn(ctx, tx, 99); return e }))
	m.scope.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instanceOn(ctx, tx, 99); return e }), ErrForbidden)
}

func TestInstanceStorageFailureLeavesProjectUnbound(t *testing.T) {
	for _, failure := range []string{"read", "create", "lock", "missing-lock"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			v := publishRuntime(t, m, db, ctx, "instance-fault", testDefinition())
			injected := errors.New("instance unavailable")
			switch failure {
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("instance-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Bindings.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Query().Remove("instance-failure")
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("instance-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Bindings.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Create().Remove("instance-failure")
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "missing-lock":
				m.principals.Lock = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e })
			if failure == "missing-lock" {
				require.ErrorIs(t, err, ErrUnavailable)
			} else {
				require.ErrorIs(t, err, injected)
			}
			if failure == "read" {
				require.NoError(t, db.Callback().Query().Remove("instance-failure"))
			}
			q := query.BindBindings(db, m.names)
			n, e := q.WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, n)
		})
	}
}
