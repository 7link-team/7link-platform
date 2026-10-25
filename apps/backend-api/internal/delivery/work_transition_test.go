package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestWorkTransitionFactsUseCurrentReadsThroughoutResultProjection(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	row := h.stored()
	queries := 0
	const hook = "test:workflow_current_facts"
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
		queries++
		locking, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		require.True(t, ok, "current read missing for %s", tx.Statement.Table)
		require.Equal(t, "SHARE", locking.Strength, tx.Statement.Table)
	}))
	t.Cleanup(func() { require.NoError(t, h.db.Callback().Query().Remove(hook)) })
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		for _, fact := range [][2]string{{"REQUIRED_FIELDS", "acceptance_criteria"}, {"REQUIRED_FIELDS", "assignee"}, {"WORK_ITEM_ROLE", "EXECUTOR"}, {"RELATED_OBJECTS", "ALL_DEPENDENCIES_DONE"}, {"EVIDENCE", "EVERY_AC_HAS_PASS_EVIDENCE"}} {
			_, err := h.m.workflowFactOn(h.ctx, tx, row, 11, fact[0], fact[1], "")
			if err != nil {
				return err
			}
		}
		return nil
	}))
	require.Greater(t, queries, 8, "exercise downstream result/evidence/verification reads")
}

func TestWorkTransitionDependencyFactCurrentReadOnRealMySQL8(t *testing.T) {
	dsn := os.Getenv("MYSQL8_DSN")
	if dsn == "" {
		t.Skip("requires the isolated real MySQL 8 gate")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	raw, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, raw.Close()) })
	raw.SetMaxOpenConns(4)
	source := (&Module{}).MigrationSource()
	source.Prefix = fmt.Sprintf("wt%d_", time.Now().UnixNano()%100000000)
	t.Cleanup(func() {
		tables, e := db.Migrator().GetTables()
		require.NoError(t, e)
		// Only this test's uniquely prefixed tables belong to this cleanup.
		require.NoError(t, db.Exec("SET FOREIGN_KEY_CHECKS=0").Error)
		for _, table := range tables {
			if strings.HasPrefix(table, source.Prefix) {
				require.NoError(t, db.Migrator().DropTable(table))
			}
		}
		require.NoError(t, db.Exec("SET FOREIGN_KEY_CHECKS=1").Error)
	})
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "mysql"))
	m, fixture, ctx, detail := assignmentHarness(t, true)
	names := query.BindNames(source.Prefix)
	projects, err := query.BindProjects(fixture, m.service.names).WithContext(ctx).Find()
	require.NoError(t, err)
	require.NoError(t, query.BindProjects(db, names).WithContext(ctx).Create(projects...))
	w := query.BindWorkItems(fixture, m.service.names)
	work, err := w.WithContext(ctx).Where(w.ID.Eq(detail.ID)).First()
	require.NoError(t, err)
	predecessor := *work
	predecessor.ID += 10000
	predecessor.Number++
	predecessor.State = "ACCEPTED"
	require.NoError(t, query.BindWorkItems(db, names).WithContext(ctx).Create(work, &predecessor))
	require.NoError(t, query.BindDependencies(db, names).WithContext(ctx).Create(&model.WorkItemDependency{TenantID: work.TenantID, ProjectID: work.ProjectID, WorkItemID: work.ID, DependsOnID: predecessor.ID}))
	tx := db.Begin(&sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	q := query.BindWorkItems(tx, names)
	old, err := q.WithContext(ctx).Where(q.ID.Eq(predecessor.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACCEPTED", old.State)
	require.NoError(t, db.Table(names.WorkItems.String()).Where("id=?", predecessor.ID).Update("state", "REWORK").Error)
	old, err = q.WithContext(ctx).Where(q.ID.Eq(predecessor.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACCEPTED", old.State, "prove the transaction snapshot is stale")
	current := &Module{service: &projectService{names: names}}
	passed, err := current.workflowFactOn(ctx, tx, work, 11, "RELATED_OBJECTS", "ALL_DEPENDENCIES_DONE", "")
	require.NoError(t, err)
	require.False(t, passed, "committed rework must prevent a transition despite an earlier accepted snapshot")
	require.NoError(t, tx.Rollback().Error)
}

func transitionFixture(t *testing.T) (*Module, *gorm.DB, context.Context, guid.ID) {
	t.Helper()
	m, db, ctx, work := assignmentHarness(t, true)
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id != 14, nil
	}
	m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 114, TenantID: tenant, PrincipalID: principal, State: "ACTIVE"}, principal == 14, nil
	}
	m.workflowSelection = func(_ context.Context, _ *gorm.DB, r WorkflowTransition) (string, error) {
		if r.ToKey == "scheduled" && r.FromKey == "ready" {
			return "READY", nil
		}
		if r.ToKey == "ready" && r.FromKey == "scheduled" {
			return "READY", nil
		}
		return "", ErrWorkPreparationState
	}
	return m, db, ctx, work.ID
}

func transitionSnapshot(t *testing.T, m *Module, db *gorm.DB, id guid.ID) string {
	t.Helper()
	var row model.WorkItem
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", id).First(&row).Error)
	var transitions []model.WorkItemTransition
	require.NoError(t, db.Table(m.service.names.WorkTransitions.String()).Where("work_item_id=?", id).Order("id").Find(&transitions).Error)
	var activities []model.WorkItemActivity
	require.NoError(t, db.Table(m.service.names.WorkActivities.String()).Where("work_item_id=?", id).Order("seq").Find(&activities).Error)
	var effects int64
	require.NoError(t, db.Table("provision_probe").Count(&effects).Error)
	data, err := json.Marshal([]any{row, transitions, activities, effects})
	require.NoError(t, err)
	return string(data)
}

func TestWorkTransitionContextPrecedesConditionsAndPersistsExactDisplayIdentity(t *testing.T) {
	m, db, ctx, id := transitionFixture(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 14, SubjectType: authn.SubjectTypePrincipal})
	calls := 0
	selectState := m.workflowSelection
	m.workflowSelection = func(ctx context.Context, tx *gorm.DB, r WorkflowTransition) (string, error) {
		calls++
		return selectState(ctx, tx, r)
	}
	apply := func(in workTransitionBody) (workTransitionView, error) {
		var out workTransitionView
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.applyWorkTransitionOn(ctx, tx, 1, 99, id, in)
			return err
		})
		return out, err
	}
	before := transitionSnapshot(t, m, db, id)
	_, err := apply(workTransitionBody{ToStateKey: "scheduled"})
	require.ErrorIs(t, err, ErrWorkSeenSeqRequired)
	require.Zero(t, calls)
	require.Equal(t, before, transitionSnapshot(t, m, db, id))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.recordWorkActivityOn(ctx, tx, 1, id, WorkActivityInput{Kind: "COMMENT_ADDED", ActorKind: "HUMAN", ActorID: 12, RefKind: "artifact_comment", RefID: 900, Title: "New source comment"})
		return err
	}))
	before = transitionSnapshot(t, m, db, id)
	seen := int64(0)
	_, err = apply(workTransitionBody{ToStateKey: "scheduled", SeenSeq: &seen})
	var interrupted *WorkWriteInterrupted
	require.ErrorAs(t, err, &interrupted)
	require.EqualValues(t, 1, interrupted.CurrentSeq)
	require.Equal(t, "COMMENT_ADDED", interrupted.NewerEvents[0].Kind)
	require.Zero(t, calls)
	require.Equal(t, before, transitionSnapshot(t, m, db, id))
	seen = 1
	first, err := apply(workTransitionBody{ToStateKey: "scheduled", SeenSeq: &seen, Comment: "Ready for the next review"})
	require.NoError(t, err)
	require.Equal(t, "scheduled", first.StateKey)
	require.Equal(t, "READY", first.State)
	require.EqualValues(t, 2, first.ActivitySeq)
	receipt, err := query.BindWorkTransitions(db, m.service.names).WithContext(ctx).Where(query.BindWorkTransitions(db, m.service.names).ID.Eq(first.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ready", *receipt.FromStateKey)
	require.Equal(t, "scheduled", *receipt.ToStateKey)
	require.Equal(t, "Ready for the next review", *receipt.Comment)
	// Own TRANSITIONED activity after seen_seq is not an interruption. A new
	// command resolves its source from persisted display identity.
	second, err := apply(workTransitionBody{ToStateKey: "ready", SeenSeq: &seen})
	require.NoError(t, err)
	require.EqualValues(t, 3, second.ActivitySeq)
}

func TestWorkTransitionReadinessUsesCanonicalCommandAndRollsBackRejectedTarget(t *testing.T) {
	m, db, ctx, work, preparation := readinessHarness(t)
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "ACTIVE").Error)
	encoded, err := json.Marshal(preparation)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	delete(fields, "confirm_contract")
	m.workflowSelection = func(context.Context, *gorm.DB, WorkflowTransition) (string, error) { return "BLOCKED", nil }
	before := transitionSnapshot(t, m, db, work.ID)
	err = db.Transaction(func(tx *gorm.DB) error {
		_, err := m.applyWorkTransitionOn(ctx, tx, 1, 99, work.ID, workTransitionBody{ToStateKey: "blocked", Fields: fields})
		return err
	})
	require.ErrorIs(t, err, ErrWorkPreparationState, "a matrix selection cannot override actual readiness")
	require.Equal(t, before, transitionSnapshot(t, m, db, work.ID))
	m.workflowSelection = func(context.Context, *gorm.DB, WorkflowTransition) (string, error) { return "READY", nil }
	var out workTransitionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.applyWorkTransitionOn(ctx, tx, 1, 99, work.ID, workTransitionBody{ToStateKey: "ready", Fields: fields})
		return err
	}))
	require.Equal(t, "READY", out.State)
	checks, err := query.BindReadinessChecks(db, m.service.names).WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, checks)
}

func TestWorkTransitionAuditFailureRollsBackDisplayVersionAndActivity(t *testing.T) {
	m, db, ctx, id := transitionFixture(t)
	before := transitionSnapshot(t, m, db, id)
	failure := errors.New("audit append failed")
	m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := m.applyWorkTransitionOn(ctx, tx, 1, 99, id, workTransitionBody{ToStateKey: "scheduled"})
		return err
	})
	require.ErrorIs(t, err, failure)
	require.Equal(t, before, transitionSnapshot(t, m, db, id))
}
