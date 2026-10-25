package templates

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func publishRuntime(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, key string, d TemplateDefinition) TemplateVersionView {
	t.Helper()
	var v TemplateVersionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.draftOn(ctx, tx, key, d)
		if e != nil {
			return e
		}
		_, e = m.publishOn(ctx, tx, v.ID, v.Hash)
		return e
	}))
	return v
}

func TestRuntimeConditionSkipsOnlyUnselectedBranchAndReplays(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	yes := d.Workflow.Nodes[0]
	yes.ID = "yes"
	no := yes
	no.ID = "no"
	no.Name = "Must not create"
	d.Workflow.Parameters = map[string]any{"method": "agile"}
	d.Workflow.EntryNodeID = "choose"
	d.Workflow.Nodes = []TemplateNode{{ID: "choose", Kind: "CONDITION", Name: "Choose", Condition: json.RawMessage(`{"==":[{"var":"parameters.method"},"agile"]}`)}, yes, no, d.Workflow.Nodes[1], d.Workflow.Nodes[2]}
	d.Workflow.Edges = []TemplateEdge{{ID: "a", Source: "choose", Target: "yes", Branch: "true"}, {ID: "b", Source: "choose", Target: "no", Branch: "false"}, {ID: "c", Source: "yes", Target: "team"}, {ID: "d", Source: "no", Target: "team"}, {ID: "e", Source: "team", Target: "client"}}
	v := publishRuntime(t, m, db, ctx, "condition", d)
	calls := 0
	m.delivery.CreateWorkOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, in delivery.TemplateWorkInput) (guid.ID, error) {
		require.NotEqual(t, "Must not create", in.Title)
		calls++
		return 700, nil
	}
	status := "IN_PROGRESS"
	m.delivery.WorkStatusOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return status, nil }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.instantiateOn(ctx, tx, v.ID, 99, 11)
		require.Equal(t, "SKIPPED", out.Runs[2].State)
		require.Equal(t, "WAITING", out.Runs[3].State)
		return e
	}))
	for i := 0; i < 3; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }))
	}
	require.Equal(t, 1, calls)
	status = "ACCEPTED"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.advanceOn(ctx, tx, 99)
		require.Equal(t, "MISSING_GATE_DECISION", out.Runs[3].Reason)
		require.Equal(t, "WAITING", out.Runs[4].State)
		return e
	}))
	require.Equal(t, 1, calls)
}

func TestRuntimeMissingFactDoesNotSelectFalseBranch(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Workflow.Nodes[0] = TemplateNode{ID: "work", Name: "Choose", Kind: "CONDITION", Condition: json.RawMessage(`{"var":"signed_brief.approved"}`)}
	d.Workflow.Edges[0].Branch = "false"
	v := publishRuntime(t, m, db, ctx, "missing", d)
	m.delivery.CreateWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TemplateWorkInput) (guid.ID, error) {
		t.Fatal("missing fact created work")
		return 0, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.instantiateOn(ctx, tx, v.ID, 99, 11)
		require.Equal(t, "MISSING_CONDITION_FACT", out.Runs[0].Reason)
		require.Equal(t, "WAITING", out.Runs[1].State)
		return e
	}))
}

func TestRuntimeStaticFanoutPinsChildAndDoesNotDuplicate(t *testing.T) {
	for _, kind := range []string{"SUBFLOW", "FAN_OUT"} {
		t.Run(kind, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			child := testDefinition()
			child.Workflow.WorkflowID = "child"
			publishRuntime(t, m, db, ctx, "child", child)
			d := testDefinition()
			d.Workflow.Nodes[0] = TemplateNode{ID: "work", Kind: kind, Name: "Expand", Subflow: &TemplateWorkflowRef{"child", 1}}
			expected := 1
			if kind == "FAN_OUT" {
				d.Workflow.Nodes[0].Items = []string{"item/a", "item:b"}
				expected = 2
			}
			v := publishRuntime(t, m, db, ctx, "parent", d)
			calls := 0
			m.delivery.CreateWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TemplateWorkInput) (guid.ID, error) {
				calls++
				return guid.ID(700 + calls), nil
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				out, e := m.instantiateOn(ctx, tx, v.ID, 99, 11)
				require.Len(t, out.Runs, 3+3*expected)
				require.Len(t, out.Runs[0].ChildHash, 64)
				return e
			}))
			require.Equal(t, expected, calls)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }))
			require.Equal(t, expected, calls)
			// Revoked visibility of a pinned child cannot be bypassed using its snapshot.
			q := query.BindVersions(db, m.names)
			_, e := q.WithContext(ctx).Where(q.Key.Eq("child")).Update(q.State, "RETIRED")
			require.NoError(t, e)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }), ErrForbidden)
		})
	}
}

func TestRuntimeProviderWritesRollbackWithAdvanceEvent(t *testing.T) {
	m, db, ctx := templateHarness(t)
	v := publishRuntime(t, m, db, ctx, "rollback", testDefinition())
	require.NoError(t, db.Exec("CREATE TABLE runtime_created_work (id INTEGER PRIMARY KEY)").Error)
	m.delivery.CreateWorkOn = func(_ context.Context, tx *gorm.DB, _, _ guid.ID, _ delivery.TemplateWorkInput) (guid.ID, error) {
		return 700, tx.Exec("INSERT INTO runtime_created_work(id) VALUES (700)").Error
	}
	original := m.audit
	m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		if event.EventType == "template.advanced" {
			return errors.New("audit failed")
		}
		return original(ctx, tx, event)
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	var count int64
	require.NoError(t, db.Table("runtime_created_work").Count(&count).Error)
	require.Zero(t, count)
	count, e := query.BindBindings(db, m.names).WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, count)
}

func TestRuntimeRejectsAmbiguousChildrenAndMalformedBranches(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	publishRuntime(t, m, db, ctx, "first", d)
	duplicate := cloneDefinition(d)
	duplicate.Workflow.Name = "Other meaning"
	var second TemplateVersionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; second, e = m.draftOn(ctx, tx, "second", duplicate); return e }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.publishOn(ctx, tx, second.ID, second.Hash); return e }), ErrConflict)
	// Historical or concurrent duplicate records must still fail closed.
	q := query.BindVersions(db, m.names)
	_, e := q.WithContext(ctx).Where(q.ID.Eq(second.ID)).Update(q.State, "PUBLISHED")
	require.NoError(t, e)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.childrenOn(ctx, tx); return e }), ErrConflict)
	w := testDefinition().Workflow
	w.Nodes[0] = TemplateNode{ID: "work", Name: "Choose", Kind: "CONDITION", Condition: json.RawMessage(`true`)}
	require.ErrorIs(t, validateRuntime(w, nil, 0), ErrInvalid)
}

func TestRuntimeLogicRejectsMissingFactsAndEnforcesBounds(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{{`{"and":[true,{">=":[2,1]}]}`, true}, {`{"or":[false,false]}`, false}, {`{"!":[false]}`, true}, {`{"in":["x",["x","y"]]}`, true}, {`{"!=":[1,2]}`, true}} {
		value, e := evalLogic(json.RawMessage(tc.raw), nil)
		require.NoError(t, e)
		require.Equal(t, tc.want, logicTruthy(value))
	}
	_, e := evalLogic(json.RawMessage(`{"var":"missing"}`), nil)
	require.ErrorIs(t, e, ErrUnavailable)
	_, e = evalLogic(json.RawMessage(`{"==":[1]}`), nil)
	require.ErrorIs(t, e, ErrInvalid)
	require.ErrorIs(t, validateRuntime(testDefinition().Workflow, nil, 17), ErrInvalid)
}

func TestRuntimeLogicScalarSemanticsAndMalformedInputs(t *testing.T) {
	cases := []struct {
		raw  string
		want any
	}{
		{`null`, nil}, {`true`, true}, {`"0"`, "0"}, {`[]`, []any{}},
		{`{"==":["1",1]}`, true}, {`{"==":["",false]}`, true}, {`{"==":[null,false]}`, false}, {`{"==":[true,1]}`, true}, {`{"==":["x","x"]}`, true}, {`{"!=":[true,false]}`, true}, {`{"==":["x",1]}`, false}, {`{"==":[null,null]}`, true},
		{`{"and":[]}`, true}, {`{"or":[]}`, false}, {`{"and":[false,{"var":"absent"}]}`, false}, {`{"or":["keep",{"var":"absent"}]}`, "keep"},
		{`{">":[3,2]}`, true}, {`{"<":[2,3]}`, true}, {`{"<=":[2,2]}`, true}, {`{">=":["2",2]}`, true}, {`{">":["b","a"]}`, true}, {`{"<":["a","b"]}`, true}, {`{"<=":["a","a"]}`, true}, {`{">=":["a","a"]}`, true},
		{`{"in":["needle","hay needle"]}`, true}, {`{"in":["missing",["x"]]}`, false}, {`{"in":[{"var":"needle"},[{"var":"match"}]]}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			v, e := evalLogic(json.RawMessage(tc.raw), map[string]any{"needle": "x", "match": "x"})
			require.NoError(t, e)
			require.Equal(t, tc.want, v)
		})
	}
	for _, raw := range []string{`{`, `{}`, `{"==":[1],"!":[true]}`, `{"var":[]}`, `{"var":""}`, `{"!":[true,false]}`, `{"==":[1]}`, `{"==":true}`, `{">":[{},1]}`, `{">":["x",1]}`, `{"in":[1,"hay"]}`, `{"in":[1,2]}`, `{"==":[[],[]]}`, `{"unknown":[]}`, `[{"var":"absent"}]`} {
		_, e := evalLogic(json.RawMessage(raw), nil)
		require.Error(t, e, raw)
	}
	_, e := evalLogic(json.RawMessage(`{"var":"needle.value"}`), map[string]any{"needle": true})
	require.ErrorIs(t, e, ErrUnavailable)
	_, e = evalValue(nil, nil, 33)
	require.ErrorIs(t, e, ErrInvalid)
	require.False(t, logicTruthy(nil))
	require.False(t, logicTruthy(float64(0)))
	require.True(t, logicTruthy(float64(1)))
	require.False(t, logicTruthy(""))
	require.True(t, logicTruthy("0"))
	require.False(t, logicTruthy([]any{}))
	require.True(t, logicTruthy([]any{1}))
	require.True(t, logicTruthy(map[string]any{}))
}

func TestRuntimeUnavailableControlsAndLegacyState(t *testing.T) {
	for _, kind := range []string{"LOOP", "FAN_OUT"} {
		t.Run(kind, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			d := testDefinition()
			node := TemplateNode{ID: "work", Kind: kind, Name: "Blocked", Condition: json.RawMessage(`true`), MaxIterations: 2, Budget: 10, EscalateTo: "accountable", Items: []string{"one"}}
			d.Workflow.Nodes[0] = node
			v := publishRuntime(t, m, db, ctx, "blocked", d)
			m.delivery.CreateWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TemplateWorkInput) (guid.ID, error) {
				t.Fatal("unsupported control created work")
				return 0, nil
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				out, e := m.instantiateOn(ctx, tx, v.ID, 99, 11)
				require.Equal(t, "WAITING", out.Runs[0].State)
				require.NotEmpty(t, out.Runs[0].Reason)
				return e
			}))
			q := query.BindBindings(db, m.names)
			_, e := q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).Update(q.Runs, `[{"node_id":"work","expansion_key":"work","state":"WAITING"}]`)
			require.NoError(t, e)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instanceOn(ctx, tx, 99); return e }))
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }), ErrUnavailable)
		})
	}
}

func TestRuntimeCommandsReauthorizeReplayAndKeepLargeReceiptsBounded(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Workflow.Parameters = map[string]any{"large": strings.Repeat("x", 80000)}
	v := publishRuntime(t, m, db, ctx, "large", d)
	created := 0
	m.delivery.CreateWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TemplateWorkInput) (guid.ID, error) {
		created++
		return 700, nil
	}
	in := &instantiateInput{ID: v.ID.String(), IdempotencyKey: "large-instance"}
	in.Body.ProjectID = 99
	in.Body.AccountableID = 11
	first, e := m.instantiateHTTP(ctx, in)
	require.NoError(t, e)
	raw, e := json.Marshal(first.Body.Data)
	require.NoError(t, e)
	require.Greater(t, len(raw), 65536)
	_, e = m.instantiateHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, 1, created)
	advance := &advanceInput{ProjectID: "99", IdempotencyKey: "advance-once"}
	_, e = m.advanceHTTP(ctx, advance)
	require.NoError(t, e)
	_, e = m.advanceHTTP(ctx, advance)
	require.NoError(t, e)
	original := m.scope.AuthorizeOn
	m.scope.AuthorizeOn = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) error {
		if permission == "project_manage" {
			return ErrForbidden
		}
		return original(ctx, tx, tenant, project, permission)
	}
	_, e = m.advanceHTTP(ctx, advance)
	assertStatus(t, e, 403)
	_, e = m.instantiateHTTP(ctx, in)
	assertStatus(t, e, 403)
	m.scope.AuthorizeOn = original
	originalPolicy := m.policy
	m.policy = func(ctx context.Context, tx *gorm.DB, tenant guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "template_read" {
			return authz.DataConstraint{}, nil
		}
		return originalPolicy(ctx, tx, tenant, permission, actor)
	}
	_, e = m.advanceHTTP(ctx, advance)
	assertStatus(t, e, 403)
	_, e = m.advanceHTTP(ctx, &advanceInput{ProjectID: "bad", IdempotencyKey: "invalid"})
	assertStatus(t, e, 422)
	_, e = m.advanceHTTP(ctx, &advanceInput{ProjectID: "98", IdempotencyKey: "missing-project"})
	assertStatus(t, e, 404)
}

func TestRuntimeDependencyFailureRollsBackOnlyNewMaterialization(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	next := d.Workflow.Nodes[0]
	next.ID = "next"
	d.Workflow.Nodes = append(d.Workflow.Nodes, next)
	d.Workflow.Edges = []TemplateEdge{{ID: "a", Source: "work", Target: "next"}, {ID: "b", Source: "next", Target: "team"}, {ID: "c", Source: "team", Target: "client"}}
	v := publishRuntime(t, m, db, ctx, "dependency", d)
	require.NoError(t, db.Exec("CREATE TABLE runtime_dependency_work (id INTEGER PRIMARY KEY)").Error)
	sequence := guid.ID(700)
	m.delivery.CreateWorkOn = func(_ context.Context, tx *gorm.DB, _, _ guid.ID, _ delivery.TemplateWorkInput) (guid.ID, error) {
		sequence++
		return sequence, tx.Exec("INSERT INTO runtime_dependency_work(id) VALUES (?)", sequence).Error
	}
	m.delivery.WorkStatusOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return "ACCEPTED", nil }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	injected := errors.New("dependency unavailable")
	m.delivery.AddDependencyOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return injected }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }), injected)
	var count int64
	require.NoError(t, db.Table("runtime_dependency_work").Count(&count).Error)
	require.Equal(t, int64(1), count)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.instanceOn(ctx, tx, 99)
		require.Nil(t, out.Runs[3].WorkItemID)
		return e
	}))
	m.delivery.AddDependencyOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, work, predecessor guid.ID) error {
		require.Equal(t, guid.ID(703), work)
		require.Equal(t, guid.ID(701), predecessor)
		return nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }))
	require.NoError(t, db.Table("runtime_dependency_work").Count(&count).Error)
	require.Equal(t, int64(2), count)
}

func TestRuntimeCurrentBindingCorruptionAndSourceFailureRollback(t *testing.T) {
	for _, failure := range []string{"runs", "hash", "lock", "work-source", "brief-source", "children-read"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			v := publishRuntime(t, m, db, ctx, "runtime-failure", testDefinition())
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
			injected := errors.New("runtime source unavailable")
			switch failure {
			case "runs":
				require.NoError(t, db.Table(m.names.Bindings.String()).Where("project_id=?", 99).Update("runs", "invalid").Error)
			case "hash":
				require.NoError(t, db.Table(m.names.Bindings.String()).Where("project_id=?", 99).Update("definition_sha256", strings.Repeat("f", 64)).Error)
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "work-source":
				m.delivery.WorkStatusOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return "", injected }
			case "brief-source":
				m.briefs.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.SignedBriefFact, bool, error) {
					return intake.SignedBriefFact{}, false, injected
				}
			case "children-read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("runtime-child-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Versions.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Query().Remove("runtime-child-failure")
			}
			var before string
			require.NoError(t, db.Table(m.names.Bindings.String()).Select("runs").Where("project_id=?", 99).Scan(&before).Error)
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e })
			require.Error(t, err)
			if failure == "hash" {
				require.ErrorIs(t, err, ErrConflict)
			} else if failure != "runs" {
				require.ErrorIs(t, err, injected)
			}
			var after string
			require.NoError(t, db.Table(m.names.Bindings.String()).Select("runs").Where("project_id=?", 99).Scan(&after).Error)
			require.Equal(t, before, after)
		})
	}
}
