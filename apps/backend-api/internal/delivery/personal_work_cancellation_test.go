package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCancellationReadFailuresPreserveProjectAndWork(t *testing.T) {
	for boundary := 1; boundary <= 30; boundary++ {
		t.Run(fmt.Sprint(boundary), func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			m.stageAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			m.scopeReauthentication = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
			a := clientWork{m: m, cancellation: &ProjectExecutionCancellation{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{11}, nil }, CancelOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }}}
			p := query.BindProjects(db, m.service.names)
			before, e := p.WithContext(ctx).First()
			require.NoError(t, e)
			w := query.BindWorkItems(db, m.service.names)
			beforeWork, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
			require.NoError(t, e)
			calls, fired := 0, false
			failure := errors.New("cancellation source read failed")
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("cancellation_read_boundary", func(tx *gorm.DB) {
				calls++
				if calls == boundary {
					fired = true
					_ = tx.AddError(failure)
				}
			}))
			err := db.Transaction(func(tx *gorm.DB) error {
				if e := a.LockCancellationOn(ctx, tx, 1, 99); e != nil {
					return e
				}
				_, e := a.CancelProjectOn(ctx, tx, 1, 99, 9001, "Human canceled project", "cookie", "proof")
				return e
			})
			require.NoError(t, db.Callback().Query().Remove("cancellation_read_boundary"))
			after, e := p.WithContext(ctx).First()
			require.NoError(t, e)
			afterWork, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
			require.NoError(t, e)
			if fired {
				require.Error(t, err)
				require.Equal(t, before, after)
				require.Equal(t, beforeWork, afterWork)
			} else {
				require.NoError(t, err)
				require.Equal(t, "CANCELLED", after.Stage)     //nolint:misspell // Frozen domain state.
				require.Equal(t, "CANCELLED", afterWork.State) //nolint:misspell // Frozen domain state.
			}
		})
	}
}

func TestProjectCancellationArchivesDraftsPreservesSentAndRollsBackEveryEffect(t *testing.T) {
	for _, fault := range []string{"", "proof", "authority", "authority_error", "authority_delegated", "stage_denied", "stage_error", "stage_policy", "execution", "work", "history", "assignment", "delivery", "project", "stage", "audit", "work_query", "assignment_query", "delivery_query", "work_id", "stage_id", "event", "missing_authority"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			assignment := assignmentClaim(t, m, db, ctx, work, false)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			failure := errors.New("cancel test fault")
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				if fault == "authority_error" {
					return governance.AuthorityDecision{}, failure
				}
				if fault == "authority_delegated" {
					return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead", DelegationID: 900}, nil
				}
				return governance.AuthorityDecision{Allowed: fault != "authority", AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			m.stageAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (governance.AuthorityDecision, error) {
				if fault == "stage_error" {
					return governance.AuthorityDecision{}, failure
				}
				return governance.AuthorityDecision{Allowed: fault != "stage_denied", AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			if fault == "stage_policy" {
				m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: permission != "project_stage_change"}, nil
				}
			}
			m.scopeReauthentication = func(_ context.Context, _ *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
				require.Equal(t, guid.ID(11), actor)
				require.Equal(t, "project_cancel", purpose)
				require.Equal(t, "project:99", target)
				require.Equal(t, "cookie", cookie)
				require.Equal(t, "proof", proof)
				if fault == "proof" {
					return failure
				}
				return nil
			}
			at := time.Now().UTC().Truncate(time.Millisecond)
			for i, state := range []string{"DRAFT", "READY", "SENT"} {
				r := &model.Delivery{TenantID: 1, ID: guid.ID(700 + i), ProjectID: 99, SequenceNo: i + 1, Number: fmt.Sprint(i + 1), Title: "Delivery", State: state, BundleState: "NONE", SendContactIDs: "[]", SourceIntakeIDs: "[]", CreatedAt: at, UpdatedAt: at, Version: 1}
				require.NoError(t, query.BindDeliveries(db, m.service.names).WithContext(ctx).Create(r))
			}
			a := clientWork{m: m, cancellation: &ProjectExecutionCancellation{
				PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{11}, nil },
				CancelOn: func(_ context.Context, tx *gorm.DB, _, _ guid.ID) error {
					if fault == "execution" {
						return failure
					}
					return nil
				},
			}}
			if fault == "audit" {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			}
			if fault == "work_id" || fault == "stage_id" {
				original := m.ids
				calls := 0
				m.ids = draftIDSource(func() (int64, error) {
					calls++
					if calls == 1 && fault == "work_id" || calls == 2 && fault == "stage_id" {
						return 0, failure
					}
					return original.Next()
				})
			}
			if fault == "event" {
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
			}
			if fault == "missing_authority" {
				m.deliveryAuthority = nil
			}
			tables := map[string]string{"work": m.service.names.WorkItems.String(), "history": m.service.names.WorkTransitions.String(), "assignment": m.service.names.Assignments.String(), "delivery": m.service.names.Deliveries.String(), "project": m.service.names.Projects.String(), "stage": m.service.names.StageTransitions.String()}
			if table := tables[fault]; table != "" {
				op := "UPDATE"
				if fault == "history" || fault == "stage" {
					op = "INSERT"
				}
				require.NoError(t, db.Exec(fmt.Sprintf("CREATE TRIGGER reject_cancellation BEFORE %s ON %s BEGIN SELECT RAISE(ABORT,'cancel test fault'); END", op, table)).Error)
			}
			q := query.BindProjects(db, m.service.names)
			before, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			err := db.Transaction(func(tx *gorm.DB) error {
				if e := a.LockCancellationOn(ctx, tx, 1, 99); e != nil {
					return e
				}
				queryTables := map[string]string{"work_query": m.service.names.WorkItems.String(), "assignment_query": m.service.names.Assignments.String(), "delivery_query": m.service.names.Deliveries.String()}
				if table := queryTables[fault]; table != "" {
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("cancel_effect_query", func(tx *gorm.DB) {
						if tx.Statement.Table == table {
							tx.AddError(failure)
						}
					}))
				}
				_, e := a.CancelProjectOn(ctx, tx, 1, 99, 901, "Human canceled project", "cookie", "proof")
				return e
			})
			if strings.HasSuffix(fault, "_query") {
				require.NoError(t, db.Callback().Query().Remove("cancel_effect_query"))
			}
			p, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			w, e := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, e)
			as, e := query.BindAssignments(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, e)
			d, e := query.BindDeliveries(db, m.service.names).WithContext(ctx).Order(query.BindDeliveries(db, m.service.names).ID).Find()
			require.NoError(t, e)
			if fault != "" {
				require.Error(t, err)
				require.Equal(t, before, p)
				require.Equal(t, "CLAIMED", w.State)
				require.Equal(t, assignment.Assignment.Version, as.Version)
				require.Equal(t, []string{"DRAFT", "READY", "SENT"}, []string{d[0].State, d[1].State, d[2].State})
				for _, row := range d {
					require.Nil(t, row.ArchivedAt)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, "CANCELLED", p.Stage) //nolint:misspell // Frozen domain state.
			require.Equal(t, "NONE", p.Health)
			require.Equal(t, "[]", p.Holds)
			require.Equal(t, "CANCELLED", w.State) //nolint:misspell // Frozen domain state.
			require.NotNil(t, w.CancelledAt)
			require.Equal(t, "RELEASED", as.Status)
			require.Nil(t, as.ActiveSlot)
			for _, row := range d[:2] {
				require.Equal(t, "DRAFT", row.State)
				require.NotNil(t, row.ArchivedAt)
			}
			require.Equal(t, "SENT", d[2].State)
			require.Nil(t, d[2].ArchivedAt)
			history, e := query.BindStageTransitions(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, guid.ID(901), *history.DecisionID)
			transitions, e := query.BindWorkTransitions(db, m.service.names).WithContext(ctx).Where(query.BindWorkTransitions(db, m.service.names).DecisionID.Eq(guid.ID(901))).Find()
			require.NoError(t, e)
			require.Len(t, transitions, 1)
		})
	}
}

func TestCancellationPrelocksSortedPrincipalsAndRejectsChangedBindings(t *testing.T) {
	for _, fault := range []string{"", "discovery", "principal", "policy", "changed", "work_query", "assignment_query"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: fault != "policy"}, nil
			}
			var locks []guid.ID
			failure := errors.New("preflight failed")
			m.principals.Lock = func(_ context.Context, tx *gorm.DB, id guid.ID) error {
				locks = append(locks, id)
				if fault == "principal" {
					return failure
				}
				if fault == "changed" && id == 11 {
					return tx.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("current_execution_id", guid.ID(999)).Error
				}
				return nil
			}
			a := clientWork{m: m, cancellation: &ProjectExecutionCancellation{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) {
				if fault == "discovery" {
					return nil, failure
				}
				return []guid.ID{20, 3, 20, 12}, nil
			}}}
			if fault == "work_query" || fault == "assignment_query" {
				table := m.service.names.WorkItems.String()
				if fault == "assignment_query" {
					table = m.service.names.Assignments.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("cancel_read_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(failure)
					}
				}))
			}
			err := db.Transaction(func(tx *gorm.DB) error { return a.LockCancellationOn(ctx, tx, 1, 99) })
			if fault != "" {
				require.Error(t, err)
				if fault == "changed" {
					require.ErrorIs(t, err, ErrWorkVersion)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, []guid.ID{3, 11, 12, 20}, locks)
			require.ErrorIs(t, a.LockCancellationOn(ctx, db, 1, 99), ErrInvalidArgument)
			require.ErrorIs(t, a.LockCancellationOn(ctx, db, 2, 99), ErrInvalidArgument)
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { return (clientWork{m: m}).LockCancellationOn(ctx, tx, 1, 99) }))
		})
	}
}

func TestCancellationConnectorRejectsIncompleteAndDuplicateProviders(t *testing.T) {
	m, db, ctx, _ := assignmentHarness(t, true)
	s := module.NewServices()
	require.NoError(t, m.provideClientWork(s))
	require.NoError(t, s.Seal())
	c, e := module.Resolve[ProjectCancellationConnector](s)
	require.NoError(t, e)
	require.Error(t, c.Bind(ProjectExecutionCancellation{}))
	require.NoError(t, c.Bind(ProjectExecutionCancellation{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }, CancelOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }}))
	require.Error(t, c.Bind(ProjectExecutionCancellation{}))
	require.Error(t, m.provideClientWork(s))
	a, e := module.Resolve[ClientWorkAccess](s)
	require.NoError(t, e)
	_, ok := a.(ProjectCancellationAccess)
	require.True(t, ok)
	require.Error(t, c.AuthorizeReceiptOn(ctx, db, ExecutionWorkLink{}))
	_, e = (clientWork{m: m}).CancelProjectOn(ctx, db, 1, 99, 1, "Cancel project now", "", "")
	require.ErrorIs(t, e, ErrInvalidArgument)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := (clientWork{m: m}).CancelProjectOn(ctx, tx, 1, 99, 1, "Cancel project now", "", "")
		return e
	}))
}

func TestCancelledWorkAcceptsExactStopAcknowledgementWithoutReopening(t *testing.T) {
	h, link := executionWorkFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
	}
	h.m.stageAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
	}
	h.m.scopeReauthentication = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		if e := h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE"); e != nil {
			return e
		}
		return h.m.startExecutionWorkOn(h.ctx, tx, link)
	}))
	a := clientWork{m: h.m, cancellation: &ProjectExecutionCancellation{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{11}, nil }, CancelOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }}}
	require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"stage": "BLOCKED", "blocked_kind": "EXTERNAL", "blocked_reason": "Awaiting external service"}).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		if e := a.LockCancellationOn(h.ctx, tx, 1, 99); e != nil {
			return e
		}
		_, e := a.CancelProjectOn(h.ctx, tx, 1, 99, 900, "Human cancels active project", "cookie", "proof")
		return e
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, _, e := h.m.executionStopWorkOn(h.ctx, tx, link, false)
		return e
	}))
	wrong := link
	wrong.ExecutionID++
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, _, e := h.m.executionStopWorkOn(h.ctx, tx, wrong, false); return e }))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.finishExecutionWorkOn(h.ctx, tx, link, ExecutionCompletion{State: "CANCELLED", Reason: "Runner confirms project cancellation"}) //nolint:misspell // Frozen protocol state.
	}))
	w := query.BindWorkItems(h.db, h.m.service.names)
	work, e := w.WithContext(h.ctx).Where(w.ID.Eq(link.WorkItemID)).First()
	require.NoError(t, e)
	require.Equal(t, "CANCELLED", work.State) //nolint:misspell // Frozen state.
	require.Nil(t, work.CurrentExecutionID)
	require.NotNil(t, work.CancelledAt)
	p := query.BindProjects(h.db, h.m.service.names)
	project, e := p.WithContext(h.ctx).First()
	require.NoError(t, e)
	require.Nil(t, project.BlockedKind)
	require.Nil(t, project.BlockedReason)
	q := query.BindAssignments(h.db, h.m.service.names)
	assignment, e := q.WithContext(h.ctx).Where(q.ID.Eq(link.AssignmentID)).First()
	require.NoError(t, e)
	require.Equal(t, "RELEASED", assignment.Status)
}
