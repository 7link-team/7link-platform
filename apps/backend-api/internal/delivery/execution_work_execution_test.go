package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func executionWorkFixture(t *testing.T) (*resultFixture, ExecutionWorkLink) {
	h := newResultFixture(t)
	approveFixtureReviewPlan(t, h)
	a, err := query.BindAssignments(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	return h, ExecutionWorkLink{TenantID: 1, ProjectID: 99, WorkItemID: h.work, AssignmentID: a.ID, ExecutionID: 90000001, ActorID: 11, InitiatorID: 11}
}

func TestAutonomyPreconditionHoldPreservesHumanInteractiveExecution(t *testing.T) {
	h, link := executionWorkFixture(t)
	require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id=?", link.ProjectID).Update("holds", `[{"kind":"AUTONOMY_PRECONDITION"}]`).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		if err := h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE"); err != nil {
			return err
		}
		if err := h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "DISPATCH"); err != nil {
			return err
		}
		return h.m.startExecutionWorkOn(h.ctx, tx, link)
	}))
}
func TestExecutionDeliveryUsesExistingResultAndIndependentReview(t *testing.T) {
	h, l := executionWorkFixture(t)
	s := module.NewServices()
	require.NoError(t, h.m.provideExecution(s))
	require.NoError(t, s.Seal())
	p, err := module.Resolve[ExecutionWork](s)
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		ids, e := p.PrincipalsOn(h.ctx, tx, l)
		require.NoError(t, e)
		require.Equal(t, []guid.ID{11}, ids)
		if e = p.AuthorizeOn(h.ctx, tx, l, "CREATE"); e != nil {
			return e
		}
		if e = p.AuthorizeOn(h.ctx, tx, l, "DISPATCH"); e != nil {
			return e
		}
		if e = p.StartedOn(h.ctx, tx, l); e != nil {
			return e
		}
		return p.StartedOn(h.ctx, tx, l)
	}))
	c, err := query.BindWorkCriteria(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	completion := ExecutionCompletion{State: "SUCCEEDED", HeadCommit: strings.Repeat("a", 40), Reason: "CLAIM_SUBMITTED", Evidence: []ExecutionEvidence{{ID: 8001, Kind: "COMMIT", Title: "Head", ContentHash: strings.Repeat("b", 64)}, {ID: 8002, Kind: "FILE", Title: "Report", ContentHash: strings.Repeat("c", 64), Artifact: &ExecutionArtifact{SpaceID: 500, FileID: 501, VersionID: 502, FileVersion: 3, CriterionID: &c.ID, Coverage: "COVERED", SHA256: strings.Repeat("a", 64)}}}}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return p.FinishedOn(h.ctx, tx, l, completion) }))
	w, err := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	require.Equal(t, "NEEDS_HUMAN_REVIEW", w.State)
	require.Nil(t, w.CurrentExecutionID)
	require.Equal(t, l.ExecutionID, *w.CurrentClaimID)
	require.Nil(t, w.AcceptedAt)
	row, err := query.BindResultVersions(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	require.Equal(t, l.ExecutionID, row.ID)
	require.Equal(t, l.InitiatorID, row.ActorID)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		v, e := h.m.resultViewOn(h.ctx, tx, row)
		require.NoError(t, e)
		require.Len(t, v.Evidence, 1)
		require.Equal(t, strings.Repeat("a", 64), v.Evidence[0].SHA256)
		require.Empty(t, v.Actions)
		require.ErrorIs(t, h.m.resultAcceptanceReadyOn(h.ctx, tx, w, row), ErrResultCoverage)
		return nil
	}))
	transitions, err := query.BindWorkTransitions(h.db, h.m.service.names).WithContext(h.ctx).Find()
	require.NoError(t, err)
	require.Equal(t, "SYSTEM", transitions[len(transitions)-1].ActorKind)
}
func TestExecutionDeliveryRevalidatesAndRollsBack(t *testing.T) {
	for _, scenario := range []string{"wrong_actor", "wrong_tenant", "initiator_revoked", "permission_revoked", "wrong_assignment", "lease_expired", "paused", "project_closed", "other_execution", "audit_failure", "bad_head", "bad_hash", "hash_mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			h, l := executionWorkFixture(t)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, l, "CREATE") }))
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.startExecutionWorkOn(h.ctx, tx, l) }))
			in := ExecutionCompletion{State: "SUCCEEDED", HeadCommit: strings.Repeat("a", 40), Evidence: []ExecutionEvidence{{ID: 8001, Kind: "COMMIT", Title: "Head", ContentHash: strings.Repeat("b", 64)}}}
			ctx := h.ctx
			switch scenario {
			case "wrong_actor":
				l.ActorID = 12
			case "wrong_tenant":
				l.TenantID = 2
			case "initiator_revoked":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "permission_revoked":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "wrong_assignment":
				l.AssignmentID++
			case "lease_expired":
				require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("id=?", l.AssignmentID).Update("lease_expires_at", time.Now().Add(-time.Hour)).Error)
			case "paused":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("paused_at", time.Now()).Error)
			case "project_closed":
				require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "CLOSED").Error)
			case "other_execution":
				l.ExecutionID++
			case "audit_failure":
				h.m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit failure") }
			case "bad_head":
				in.HeadCommit = "bogus"
			case "bad_hash":
				in.Evidence[0].ContentHash = strings.Repeat("z", 64)
			case "hash_mismatch":
				in.Evidence[0].Artifact = &ExecutionArtifact{SpaceID: 500, FileID: 501, VersionID: 502, FileVersion: 3, Coverage: "COVERED", SHA256: strings.Repeat("b", 64)}
			}
			require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.finishExecutionWorkOn(ctx, tx, l, in) }))
			count, err := query.BindResultVersions(h.db, h.m.service.names).WithContext(h.ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count)
			w, err := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).First()
			require.NoError(t, err)
			require.Equal(t, "IN_PROGRESS", w.State)
			require.NotNil(t, w.CurrentExecutionID)
			require.Nil(t, w.CurrentClaimID)
			_ = authn.PrincipalIDFromContext(ctx)
		})
	}
}
func TestExecutionDeliveryCancellationNeverSubmitsAResult(t *testing.T) {
	h, l := executionWorkFixture(t)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		if e := h.m.authorizeExecutionWorkOn(h.ctx, tx, l, "CREATE"); e != nil {
			return e
		}
		return h.m.finishExecutionWorkOn(h.ctx, tx, l, ExecutionCompletion{State: "CANCELLED", Reason: "USER_CANCEL"}) //nolint:misspell // Spec freezes this protocol state spelling.
	}))
	w, err := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	require.Equal(t, "CLAIMED", w.State)
	require.Nil(t, w.CurrentExecutionID)
	require.Nil(t, w.CurrentClaimID)
}

func TestExecutionDeliveryHoldsFenceNewWorkButAllowAuthorizedStop(t *testing.T) {
	for _, hold := range []string{"BUDGET", "MANUAL", "ESCALATION"} {
		for _, operation := range []string{"CREATE", "DISPATCH", "RECOVER", "START"} {
			t.Run(hold+"/"+operation, func(t *testing.T) {
				h, l := executionWorkFixture(t)
				if operation != "CREATE" {
					require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, l, "CREATE") }))
				}
				require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id=?", l.ProjectID).Update("holds", `[{"kind":"`+hold+`"}]`).Error)
				err := h.db.Transaction(func(tx *gorm.DB) error {
					if operation == "START" {
						return h.m.startExecutionWorkOn(h.ctx, tx, l)
					}
					return h.m.authorizeExecutionWorkOn(h.ctx, tx, l, operation)
				})
				require.ErrorIs(t, err, ErrWorkProjectReadOnly)
			})
		}
	}
	h, l := executionWorkFixture(t)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, l, "CREATE") }))
	require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id=?", l.ProjectID).Updates(map[string]any{"stage": "BLOCKED", "blocked_reason": "Operator stopped new work"}).Error)
	require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("id=?", l.AssignmentID).Update("lease_expires_at", time.Now().Add(-time.Hour)).Error)
	l.InitiatorID = 999 // The current operator's authority must survive an inactive initiator.
	h.m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, "execution_stop", permission)
		require.Equal(t, l.ActorID, actor)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		if err := h.m.authorizeExecutionWorkOn(h.ctx, tx, l, "CANCEL"); err != nil {
			return err
		}
		return h.m.finishExecutionWorkOn(h.ctx, tx, l, ExecutionCompletion{State: "CANCELLED", Reason: "USER_CANCEL"}) //nolint:misspell // Spec freezes this protocol state spelling.
	}))
	count, err := query.BindResultVersions(h.db, h.m.service.names).WithContext(h.ctx).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestRealtimeProjectTopicsRecheckCurrentOwnerReadPolicy(t *testing.T) {
	h, _ := executionWorkFixture(t)
	router, err := gormx.NewDatasourceRouter(h.ctx, false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: h.db.Dialector.(*sqlite.Dialector).DSN, Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	h.m.runtime = appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router))
	h.m.projectAccess = core.PublishedAccessTableNames("d_")
	require.NoError(t, h.db.Exec("CREATE TABLE d_iam_entities (tenant_id INTEGER NOT NULL,id INTEGER NOT NULL,name TEXT NOT NULL,type TEXT NOT NULL,PRIMARY KEY(tenant_id,id))").Error)
	require.NoError(t, h.db.Exec("INSERT INTO d_iam_entities VALUES (1,99,'Project','project')").Error)
	constraint := authz.DataConstraint{AllowAll: true}
	h.m.listConstraint = func(_ context.Context, tenant guid.ID, permission string, principal guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(11), principal)
		require.Equal(t, "project_read", permission)
		return constraint, nil
	}
	for _, collection := range []bool{false, true} {
		allowed, err := h.m.authorizeRealtimeProject(h.ctx, 1, 11, 99, collection)
		require.NoError(t, err)
		require.True(t, allowed)
		constraint = authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}
		allowed, err = h.m.authorizeRealtimeProject(h.ctx, 1, 11, 99, collection)
		require.NoError(t, err)
		require.False(t, allowed)
		constraint = authz.DataConstraint{}
		allowed, err = h.m.authorizeRealtimeProject(h.ctx, 1, 11, 99, collection)
		require.NoError(t, err)
		require.False(t, allowed)
		constraint = authz.DataConstraint{AllowAll: true}
	}
	allowed, err := h.m.authorizeRealtimeProject(h.ctx, 2, 11, 99, false)
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, err = h.m.authorizeRealtimeProject(h.ctx, 1, 12, 99, false)
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, err = h.m.authorizeRealtimeProject(h.ctx, 1, 11, 100, false)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestRealtimeWorkTopicsUseActualTenantRowAndCurrentWorkReadScope(t *testing.T) {
	h, l := executionWorkFixture(t)
	router, err := gormx.NewDatasourceRouter(h.ctx, false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: h.db.Dialector.(*sqlite.Dialector).DSN, Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	h.m.runtime = appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router))
	constraint := authz.DataConstraint{AllowAll: true}
	h.m.workPolicy = func(_ context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, principal guid.ID) (authz.DataConstraint, error) {
		require.NotNil(t, tx)
		require.Equal(t, l.TenantID, tenant)
		require.Equal(t, l.ProjectID, project)
		require.Equal(t, guid.ID(11), principal)
		require.Equal(t, "work_item_read", permission)
		return constraint, nil
	}
	allowed, err := h.m.authorizeRealtimeWork(h.ctx, l.TenantID, 11, l.WorkItemID, false)
	require.NoError(t, err)
	require.True(t, allowed)
	for _, c := range []authz.DataConstraint{{}, {AllowAll: true, DeniedIDs: []string{l.ProjectID.String()}}, {OwnerIDs: []string{"999"}}} {
		constraint = c
		allowed, err = h.m.authorizeRealtimeWork(h.ctx, l.TenantID, 11, l.WorkItemID, false)
		require.NoError(t, err)
		require.False(t, allowed)
	}
	constraint = authz.DataConstraint{ResourceIDs: []string{l.ProjectID.String()}}
	allowed, err = h.m.authorizeRealtimeWork(h.ctx, l.TenantID, 11, l.WorkItemID, false)
	require.NoError(t, err)
	require.True(t, allowed)
	for _, args := range []struct {
		tenant, actor, id guid.ID
		collection        bool
	}{{2, 11, l.WorkItemID, false}, {1, 12, l.WorkItemID, false}, {1, 11, 999999, false}, {1, 11, l.WorkItemID, true}} {
		allowed, err = h.m.authorizeRealtimeWork(h.ctx, args.tenant, args.actor, args.id, args.collection)
		require.NoError(t, err)
		require.False(t, allowed)
	}
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", l.WorkItemID).Updates(map[string]any{"state": "CANCELLED", "archived_at": time.Now()}).Error) //nolint:misspell // Spec freezes this protocol state spelling.
	allowed, err = h.m.authorizeRealtimeWork(h.ctx, l.TenantID, 11, l.WorkItemID, false)
	require.NoError(t, err)
	require.False(t, allowed)
}
