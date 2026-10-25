package execution

import (
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func qualityFixture(t *testing.T) (*executionFixture, context.Context, QualityLaunchRequest) {
	f := executionHarness(t)
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
	f.m.work.PrincipalsOn = func(context.Context, *gorm.DB, WorkLink) ([]guid.ID, error) { return []guid.ID{21, 11}, nil }
	f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{TenantID: 7, PrincipalID: 21, State: "ACTIVE", RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "source-family", Roles: []string{"EXECUTOR"}}, true, nil
	}
	old := f.m.machines.LockOn
	f.m.machines.LockOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.MachineFact, error) {
		m, e := old(ctx, tx, tenant, id)
		m.SlotCount = 1
		return m, e
	}
	parent := f.create(t)
	q := query.BindExecutions(f.db, f.m.names)
	_, err := q.WithContext(t.Context()).Where(q.ID.Eq(parent.ID)).Update(q.State, "SUCCEEDED")
	require.NoError(t, err)
	in := QualityLaunchRequest{TenantID: 7, ProjectID: 9, WorkItemID: 3, ParentExecutionID: parent.ID, SourceDecisionID: 91, HeadCommit: strings.Repeat("b", 40), Feedback: `{"action":"REQUEST_CHANGES","category":"FUNCTIONAL_DEFECT"}`}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7})
	original := f.m.work.AuthorizeOn
	f.m.work.AuthorizeOn = func(ctx context.Context, tx *gorm.DB, l WorkLink, op string) error {
		if err := original(ctx, tx, l, op); err != nil {
			return err
		}
		if l.SourceDecisionID > 0 && op == "CREATE" && (l.ExecutionID <= 0 || l.ActorID != 0 || l.InitiatorID != 0 || l.ParentExecutionID != in.ParentExecutionID || l.SourceHeadCommit != in.HeadCommit || l.SourceFeedback != in.Feedback) {
			return ErrForbidden
		}
		return nil
	}
	return f, ctx, in
}

func TestQualityReworkRefreshesAutomaticKnowledgeUsingNewFeedback(t *testing.T) {
	f, ctx, in := qualityFixture(t)
	q := query.BindExecutions(f.db, f.m.names)
	parent, err := q.WithContext(t.Context()).Where(q.ID.Eq(in.ParentExecutionID)).First()
	require.NoError(t, err)
	old, err := view(parent)
	require.NoError(t, err)
	old.Plan.ContextRefs = []ContextRef{{Kind: "KNOWLEDGE", Ref: 999}}
	old.Plan.Knowledge = []knowledge.ContextItem{{EntryID: 999, Label: "[K1]", Body: "Old stale selection"}}
	encoded, err := json.Marshal(old.Plan)
	require.NoError(t, err)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", parent.ID).Updates(map[string]any{"plan_json": string(encoded), "plan_hash": hash(encoded)}).Error)
	f.m.knowledge.work.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }
	calls := 0
	readCalls := 0
	f.m.knowledge.context.AuthorizeSnapshotOn = func(ctx context.Context, _ *gorm.DB, tenant, project guid.ID, items []knowledge.ContextItem) error {
		readCalls++
		require.Equal(t, guid.ID(21), authn.PrincipalIDFromContext(ctx))
		require.Equal(t, []guid.ID{7, 9}, []guid.ID{tenant, project})
		require.Equal(t, []knowledge.ContextItem{{EntryID: 701, Label: "[R1]", Title: "New rule", Body: "Refreshed rework rule"}}, items)
		return nil
	}
	f.m.knowledge.context.BuildInjectionOn = func(ctx context.Context, _ *gorm.DB, req knowledge.InjectionRequest) (knowledge.InjectionResult, error) {
		calls++
		require.Equal(t, guid.ID(21), authn.PrincipalIDFromContext(ctx))
		require.NotEqual(t, in.ParentExecutionID, req.ExecutionID)
		require.Equal(t, []string{in.Feedback}, req.Query.Feedback)
		require.Empty(t, req.ExplicitIDs)
		return knowledge.InjectionResult{Rules: []knowledge.InjectionItem{{ContextItem: knowledge.ContextItem{EntryID: 701, Label: "[R1]", Title: "New rule", Body: "Refreshed rework rule"}}}}, nil
	}
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var err error; id, err = f.m.launchQualityOn(ctx, tx, in); return err }))
	child, err := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	frozen, err := view(child)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, readCalls)
	require.Len(t, frozen.Plan.Knowledge, 1)
	require.Equal(t, guid.ID(701), frozen.Plan.Knowledge[0].EntryID)
	require.Equal(t, []ContextRef{{Kind: "KNOWLEDGE", Ref: 701}}, frozen.Plan.ContextRefs)
}
func TestQualityLaunchSynchronousSystemContinuationReplayAndRunnerLifecycle(t *testing.T) {
	f, ctx, in := qualityFixture(t)
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		ids, e := f.m.qualityPrincipalsOn(ctx, tx, 7, in.ParentExecutionID)
		require.NoError(t, e)
		require.Equal(t, []guid.ID{11, 21, 101}, ids)
		for _, p := range ids {
			require.NoError(t, f.m.principals.Lock(ctx, tx, p))
		}
		f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return errors.New("unexpected new principal lock") }
		id, e = f.m.launchQualityOn(ctx, tx, in)
		return e
	}))
	q := query.BindExecutions(f.db, f.m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Zero(t, row.InitiatorID)
	require.Equal(t, in.ParentExecutionID, *row.ParentExecutionID)
	require.Equal(t, in.SourceDecisionID, *row.SourceDecisionID)
	require.Equal(t, "QUALITY", row.BranchReason)
	require.Equal(t, "CONTINUE", row.StartPoint)
	require.True(t, row.Autostart)
	require.Equal(t, in.Feedback, row.FeedbackSnapshot)
	v, e := view(row)
	require.NoError(t, e)
	require.Equal(t, in.HeadCommit, v.Plan.BaseCommit)
	require.Equal(t, guid.ID(21), v.Plan.ExecutorPrincipalID)
	require.Equal(t, validCreate().Plan.Objective, v.Plan.Objective)
	require.Equal(t, in.Feedback, v.Plan.QualityFeedback)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		again, e := f.m.launchQualityOn(ctx, tx, in)
		require.Equal(t, id, again)
		return e
	}))
	f.m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error { require.Positive(t, id); return nil }
	f.pull(t, id)
	_, e = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, e)
	_, e = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Child commit", Content: strings.Repeat("c", 40)})
	require.NoError(t, e)
	_, e = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "COMPLETED", HeadCommit: strings.Repeat("c", 40)})
	require.NoError(t, e)
}
func TestQualityLaunchRejectsInvalidSourcesCapacityAndRollsBack(t *testing.T) {
	for _, fault := range []string{"human", "tenant", "head", "feedback", "parent_scope", "parent_state", "identity", "assignment", "capacity", "workspace", "budget", "audit", "event", "objective"} {
		t.Run(fault, func(t *testing.T) {
			f, ctx, in := qualityFixture(t)
			failure := errors.New("test failure")
			switch fault {
			case "human":
				ctx = f.human
			case "tenant":
				ctx = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 8})
			case "head":
				in.HeadCommit = "bad"
			case "feedback":
				in.Feedback = ""
			case "parent_scope":
				in.WorkItemID = 99
			case "parent_state":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", in.ParentExecutionID).Update("state", "RUNNING").Error)
			case "identity":
				f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 0, nil }
			case "assignment":
				f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return ErrForbidden }
			case "capacity":
				old := f.m.machines.LockOn
				f.m.machines.LockOn = func(c context.Context, tx *gorm.DB, a, b guid.ID) (fleet.MachineFact, error) {
					m, e := old(c, tx, a, b)
					m.SlotCount = 0
					return m, e
				}
			case "workspace":
				f.m.machines.ResolveWorkspaceOn = nil
			case "budget":
				f.m.work.BudgetOn = func(context.Context, *gorm.DB, WorkLink) (BudgetFact, error) { return BudgetFact{}, failure }
			case "audit":
				f.m.audit = nil
			case "event":
				f.pub.fail = true
			case "objective":
				in.Feedback = strings.Repeat("x", 20001)
			}
			var before int64
			require.NoError(t, f.db.Table("effects").Count(&before).Error)
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.launchQualityOn(ctx, tx, in); return e }))
			var n int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&n).Error)
			require.EqualValues(t, 1, n)
			require.NoError(t, f.db.Table("effects").Count(&n).Error)
			require.Equal(t, before, n)
		})
	}
}
func TestQualityLaunchRejectsConflictingReplay(t *testing.T) {
	f, ctx, in := qualityFixture(t)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.launchQualityOn(ctx, tx, in); return e }))
	f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return nil }
	in.Feedback += " changed"
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.launchQualityOn(ctx, tx, in); return e }), ErrConflict)
}

func TestQualityCapacityCountsActualActiveExecutionsAndRevalidatesReplay(t *testing.T) {
	f, ctx, in := qualityFixture(t)
	other := f.create(t)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.launchQualityOn(ctx, tx, in); return e }), ErrQueueFull)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", other.ID).Update("state", "FAILED").Error)
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.launchQualityOn(ctx, tx, in); return e }))
	f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return ErrForbidden }
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.launchQualityOn(ctx, tx, in); return e }), ErrForbidden)
	require.Positive(t, id)
}
func TestQualitySystemPrincipalExceptionRequiresPersistedExactLineage(t *testing.T) {
	f, ctx, in := qualityFixture(t)
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.launchQualityOn(ctx, tx, in); return e }))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		row, e := f.m.rowOn(ctx, tx, 7, id, false)
		require.NoError(t, e)
		l := link(row, 101)
		require.NoError(t, f.m.workPrincipalLocks(ctx, tx, l, 0, 101))
		l.SourceDecisionID++
		require.ErrorIs(t, f.m.workPrincipalLocks(ctx, tx, l, 0, 101), ErrForbidden)
		l = link(row, 101)
		l.ExecutionID = in.ParentExecutionID
		require.ErrorIs(t, f.m.workPrincipalLocks(ctx, tx, l, 0, 101), ErrForbidden)
		l = link(row, 101)
		l.SourceDecisionID = 0
		require.ErrorIs(t, f.m.workPrincipalLocks(ctx, tx, l, 0, 101), ErrForbidden)
		return nil
	}))
}

func TestQualityPreflightPropagatesFailuresBeforeBusinessLocks(t *testing.T) {
	for _, fault := range []string{"parent", "capability", "machine", "assignment", "principal"} {
		t.Run(fault, func(t *testing.T) {
			f, ctx, in := qualityFixture(t)
			failure := errors.New("preflight unavailable")
			switch fault {
			case "parent":
				in.ParentExecutionID = 999
			case "capability":
				f.m.work.PrincipalsOn = nil
			case "machine":
				f.m.machines.ByIDOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, failure
				}
			case "assignment":
				f.m.work.PrincipalsOn = func(context.Context, *gorm.DB, WorkLink) ([]guid.ID, error) { return nil, failure }
			case "principal":
				f.m.work.PrincipalsOn = func(context.Context, *gorm.DB, WorkLink) ([]guid.ID, error) { return []guid.ID{0}, nil }
			}
			f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error {
				t.Fatal("preflight must not acquire principal or business locks")
				return failure
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.qualityPrincipalsOn(ctx, tx, 7, in.ParentExecutionID); return e }))
		})
	}
}
func TestQualityLaunchPropagatesStorageAndIdentityFailure(t *testing.T) {
	for _, fault := range []string{"parent", "ids", "identity", "workspace", "machine", "reserve", "insert", "parent_hash", "replay_hash", "replay_head"} {
		t.Run(fault, func(t *testing.T) {
			f, ctx, in := qualityFixture(t)
			failure := errors.New("quality dependency failed")
			if strings.HasPrefix(fault, "replay_") {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.launchQualityOn(ctx, tx, in); return e }))
			}
			switch fault {
			case "parent":
				in.ParentExecutionID = 999
			case "ids":
				f.m.ids = failedExecutionIDs{failure}
			case "identity":
				f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 0, failure }
			case "workspace":
				f.m.machines.ResolveWorkspaceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (fleet.WorkspaceFact, error) {
					return fleet.WorkspaceFact{}, failure
				}
			case "machine":
				f.m.machines.LockOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, failure
				}
			case "reserve":
				f.m.machines.ReserveSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.SlotFact, error) {
					return fleet.SlotFact{}, failure
				}
			case "insert":
				require.NoError(t, f.db.Exec("CREATE TRIGGER quality_insert BEFORE INSERT ON ex_execution_executions BEGIN SELECT RAISE(ABORT,'quality insert rejected'); END").Error)
			case "parent_hash":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", in.ParentExecutionID).Update("plan_hash", "bad").Error)
			case "replay_hash":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("source_decision_id = ?", in.SourceDecisionID).Update("plan_hash", "bad").Error)
			case "replay_head":
				f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return nil }
				in.HeadCommit = strings.Repeat("d", 40)
			}
			var before int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&before).Error)
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.launchQualityOn(ctx, tx, in); return e }))
			var after int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&after).Error)
			require.Equal(t, before, after)
		})
	}
}

func TestQualityMigrationRejectsInvalidLineageAndDestructiveRollback(t *testing.T) {
	f, ctx, in := qualityFixture(t)
	for _, values := range []map[string]any{
		{"parent_execution_id": 0},
		{"source_decision_id": 0},
		{"parent_execution_id": in.ParentExecutionID},
		{"source_decision_id": in.SourceDecisionID},
	} {
		require.Error(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", in.ParentExecutionID).Updates(values).Error)
	}
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.launchQualityOn(ctx, tx, in); return e }))
	q := query.BindExecutions(f.db, f.m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	row.ID++
	require.Error(t, q.WithContext(t.Context()).Create(row))
	raw, e := f.db.DB()
	require.NoError(t, e)
	source := f.m.MigrationSource()
	source.Prefix = "ex_"
	require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, "sqlite", 2))
	persisted, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Equal(t, in.SourceDecisionID, *persisted.SourceDecisionID)
	require.Equal(t, in.Feedback, persisted.FeedbackSnapshot)
}

func TestQualityInterruptedChildRetainsSystemProvenanceDuringHumanRecovery(t *testing.T) {
	f, ctx, in := qualityFixture(t)
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.launchQualityOn(ctx, tx, in); return e }))
	leased := f.pull(t, id)
	_, e := f.receipt(id, ReceiptInput{DispatchEpoch: leased.DispatchEpoch, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, e)
	_, e = f.receipt(id, ReceiptInput{DispatchEpoch: leased.DispatchEpoch, Seq: 2, Kind: "LOST"})
	require.NoError(t, e)
	interrupted := f.read(t, id)
	require.Equal(t, "INTERRUPTED", interrupted.State)
	original := f.m.work.AuthorizeOn
	recovered := false
	f.m.work.AuthorizeOn = func(c context.Context, tx *gorm.DB, l WorkLink, op string) error {
		if op == "RECOVER" {
			recovered = true
			require.Equal(t, guid.ID(11), l.ActorID)
			require.Zero(t, l.InitiatorID)
			require.Equal(t, in.ParentExecutionID, l.ParentExecutionID)
			require.Equal(t, in.SourceDecisionID, l.SourceDecisionID)
			require.Equal(t, guid.ID(11), authn.PrincipalIDFromContext(c))
		}
		return original(c, tx, l, op)
	}
	var resumed ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		resumed, e = f.m.RecoverOn(f.human, tx, 7, id, interrupted.Version, true)
		return e
	}))
	require.True(t, recovered)
	require.Equal(t, "CREATED", resumed.State)
	require.Equal(t, interrupted.DispatchEpoch+1, resumed.DispatchEpoch)
	require.Equal(t, interrupted.PlanHash, resumed.PlanHash)
	require.Equal(t, in.Feedback, resumed.FeedbackSnapshot)
	require.Equal(t, in.SourceDecisionID, *resumed.SourceDecisionID)
	q := query.BindExecutions(f.db, f.m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Zero(t, row.InitiatorID)
	_, e = f.receipt(id, ReceiptInput{DispatchEpoch: leased.DispatchEpoch, Seq: 3, Kind: "HEARTBEAT"})
	require.ErrorIs(t, e, ErrFenced)
}

func TestQualityMaximumObjectiveAndFeedbackRemainSeparateAcrossRounds(t *testing.T) {
	f, ctx, in := qualityFixture(t)
	q := query.BindExecutions(f.db, f.m.names)
	parent, e := q.WithContext(t.Context()).Where(q.ID.Eq(in.ParentExecutionID)).First()
	require.NoError(t, e)
	v, e := view(parent)
	require.NoError(t, e)
	objective := strings.Repeat("x", 20000)
	v.Plan.Objective = objective
	v.Plan.QualityFeedback = "previous obsolete round feedback"
	raw, e := json.Marshal(v.Plan)
	require.NoError(t, e)
	_, e = q.WithContext(t.Context()).Where(q.ID.Eq(parent.ID)).Updates(map[string]any{"plan_json": string(raw), "plan_hash": hash(raw)})
	require.NoError(t, e)
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.launchQualityOn(ctx, tx, in); return e }))
	child, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	cv, e := view(child)
	require.NoError(t, e)
	require.Equal(t, objective, cv.Plan.Objective)
	require.Equal(t, in.Feedback, cv.Plan.QualityFeedback)
	require.NotContains(t, cv.Plan.QualityFeedback, "obsolete")
}

func TestHumanCreateCannotSupplyQualitySourceFeedback(t *testing.T) {
	f := executionHarness(t)
	in := validCreate()
	in.Plan.QualityFeedback = "forged SYSTEM feedback"
	var got ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; got, e = f.m.CreateOn(f.human, tx, 7, in); return e }))
	require.Empty(t, got.Plan.QualityFeedback)
}

func TestProvideQualityLauncherRejectsDuplicateCapabilityOwnership(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.provideQualityLauncher(s))
	require.ErrorIs(t, f.m.provideQualityLauncher(s), module.ErrServiceDuplicate)
}

func TestQualityContinuationStorageFailuresPreserveCanonicalDecision(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f, ctx, in := qualityFixture(t)
		return f, func(tx *gorm.DB) error {
			id, e := f.m.launchQualityOn(ctx, tx, in)
			if e == nil {
				require.Positive(t, id)
				require.NotEqual(t, in.ParentExecutionID, id)
			}
			return e
		}
	})
}
