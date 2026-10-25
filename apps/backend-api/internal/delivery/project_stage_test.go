package delivery

import (
	"7link.com/platform/internal/delivery/model"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm/clause"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func stageHarness(t *testing.T) (*Module, *gorm.DB, context.Context, workDetailView) {
	t.Helper()
	m, db, ctx, work, _ := readinessHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.stageAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
	}
	m.sources.ProjectFactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, *guid.ID) (intake.ProjectFacts, error) {
		return intake.ProjectFacts{}, nil
	}
	m.projectAccess = core.PublishedAccessTableNames("d_")
	require.NoError(t, db.Exec("CREATE TABLE d_iam_tenant_members (tenant_id BIGINT, principal_id BIGINT, status VARCHAR(16))").Error)
	require.NoError(t, db.Exec("INSERT INTO d_iam_tenant_members VALUES (1,11,'active'),(2,22,'active')").Error)
	return m, db, ctx, work
}

func TestProjectStagesIndependentWorkRealLifecycle(t *testing.T) {
	m, db, ctx, work := stageHarness(t)
	call := func(version int, target, reason string) (stageReceipt, error) {
		var result stageReceipt
		candidates, err := m.stageExecutorCandidates(ctx, db, 1, 99)
		if err != nil {
			return result, err
		}
		require.Equal(t, []guid.ID{11}, candidates)
		err = db.Transaction(func(tx *gorm.DB) error {
			var e error
			result, e = m.changeProjectStageOn(ctx, tx, 1, 99, 11, version, stageChangeBody{ToStage: target, Reason: reason}, candidates)
			return e
		})
		return result, err
	}
	_, err := call(1, "ACTIVE", "")
	require.ErrorIs(t, err, ErrStageIllegal)
	one, err := call(1, "DISCOVERY", "")
	require.NoError(t, err)
	require.Equal(t, 2, one.Project.Version)
	_, err = call(2, "PLANNING", "")
	require.ErrorIs(t, err, ErrStageCriteria)
	_, _, _, _, preparation := readinessHarness(t)
	// The new fixture has different IDs; use this work's stable criterion ID.
	criteria, e := query.BindWorkCriteria(db, m.service.names).WithContext(ctx).Find()
	require.NoError(t, e)
	preparation.VerificationPlan[0].CriterionID = criteria[0].ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.prepareWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, work.Version, &preparation)
		return e
	}))
	two, err := call(2, "PLANNING", "")
	require.NoError(t, err)
	_, err = call(two.Project.Version, "DISCOVERY", "short")
	require.ErrorIs(t, err, ErrStageReason)
	_, err = call(two.Project.Version, "ACTIVE", "")
	require.ErrorIs(t, err, ErrStageCriteria)
	v := constitutionProposal(t, m, db, ctx)
	v = constitutionChange(t, m, db, ctx, v, nil)
	constitutionChange(t, m, db, ctx, v, &constitutionDecisionBody{Result: "APPROVE"})
	_, err = call(two.Project.Version, "ACTIVE", "")
	require.ErrorIs(t, err, ErrWorkVersion)
	active, err := call(two.Project.Version+1, "ACTIVE", "")
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", active.Project.Stage)
	require.NotNil(t, active.Project.StartedAt)
	started := active.Project.StartedAt
	kind := "DEPENDENCY"
	var blocked stageReceipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		blocked, e = m.changeProjectStageOn(ctx, tx, 1, 99, 11, active.Project.Version, stageChangeBody{ToStage: "BLOCKED", Reason: "Await upstream dependency", BlockedKind: &kind}, nil)
		return e
	}))
	resumed, err := call(blocked.Project.Version, "ACTIVE", "Dependency was delivered")
	require.NoError(t, err)
	require.Equal(t, started, resumed.Project.StartedAt)
	require.Nil(t, resumed.Project.BlockedKind)
	count, e := query.BindStageTransitions(db, m.service.names).WithContext(ctx).Count()
	require.NoError(t, e)
	require.EqualValues(t, 5, count)
	claim := assignmentClaim(t, m, db, ctx, func() workDetailView {
		v, e := m.readWork(ctx, db, 1, 99, work.ID, 11)
		require.NoError(t, e)
		return v
	}(), false)
	require.Equal(t, "CLAIMED", claim.WorkItem.State)
}

func TestProjectStageRejectionAndAtomicity(t *testing.T) {
	failure := errors.New("injected stage failure")
	for _, scenario := range []string{"authority", "scope", "inactive", "stale", "audit", "outbox", "history", "source", "open_clarification"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := stageHarness(t)
			expected := 1
			target := "DISCOVERY"
			switch scenario {
			case "authority":
				m.stageAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{}, nil
				}
			case "scope":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}, nil
				}
			case "inactive":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "stale":
				expected = 9
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
			case "history":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_stage BEFORE INSERT ON d_delivery_project_stage_transitions BEGIN SELECT RAISE(ABORT,'history unavailable'); END").Error)
			case "source":
				m.sources.ProjectFactsOn = nil
			case "open_clarification":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "DISCOVERY").Error)
				target = "PLANNING"
				m.sources.ProjectFactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, *guid.ID) (intake.ProjectFacts, error) {
					return intake.ProjectFacts{OpenClarifications: 1}, nil
				}
			}
			before, e := query.BindProjects(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, e)
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.changeProjectStageOn(ctx, tx, 1, 99, 11, expected, stageChangeBody{ToStage: target}, nil)
				return e
			})
			require.Error(t, err)
			after, e := query.BindProjects(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, before, after)
			count, e := query.BindStageTransitions(db, m.service.names).WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, count)
		})
	}
}

func TestProjectStageRequiresEveryRegisteredRequirementBaseline(t *testing.T) {
	m, db, ctx, _ := stageHarness(t)
	body := requirementCreateBody{Title: "First requirement", Criteria: []requirementCriterionInput{{Text: "Actual acceptance", Required: true}}}
	create := func() RequirementView {
		var v RequirementView
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.createRequirementOn(ctx, tx, 1, 99, 11, body); return e }))
		return v
	}
	first, second := create(), create()
	baseline := func(v RequirementView) {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.baselineRequirementOn(ctx, tx, 1, 99, v.ID, 11, v.Version)
			return e
		}))
	}
	baseline(first)
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "DISCOVERY").Error)
	advance := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.changeProjectStageOn(ctx, tx, 1, 99, 11, 1, stageChangeBody{ToStage: "PLANNING"}, nil)
			return e
		})
	}
	require.ErrorIs(t, advance(), ErrStageCriteria, "one baselined Requirement cannot hide another draft")
	baseline(second)
	require.NoError(t, advance())
}

func TestStageExecutorUsesCurrentHumanAgentModeAndScope(t *testing.T) {
	for _, scenario := range []string{"human", "agent", "human_only_agent", "inactive_member", "denied_scope", "inactive_agent"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := stageHarness(t)
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Updates(map[string]any{"state": "READY", "readiness_ok": true}).Error)
			if scenario == "agent" || scenario == "human_only_agent" || scenario == "inactive_agent" {
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				m.agents = fleet.AgentDirectory{ByPrincipalOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					state := "ACTIVE"
					if scenario == "inactive_agent" {
						state = "PAUSED"
					}
					return fleet.AgentIdentity{ID: 111, TenantID: 1, PrincipalID: 11, State: state}, true, nil
				}}
			}
			if scenario == "inactive_member" {
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
			}
			if scenario == "denied_scope" {
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}, nil
				}
			}
			var eligible bool
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				eligible, e = m.stageExecutorOn(ctx, tx, 1, 99, []guid.ID{11}, scenario == "human_only_agent")
				return e
			}))
			require.Equal(t, scenario == "human" || scenario == "agent", eligible)
		})
	}
}

// An IAM snapshot must not hide work committed while the command waits on the
// project lock. This exercises the real command against InnoDB REPEATABLE READ.
func TestProjectStageCurrentFactsExternalDatabase(t *testing.T) {
	dialect, dsn := os.Getenv("IAM_LIFECYCLE_DATABASE_TYPE"), os.Getenv("IAM_LIFECYCLE_DATABASE_DSN")
	if dialect == "" && dsn == "" {
		t.Skip("external database supplied by database gate")
	}
	require.Contains(t, []string{"mysql", "postgres"}, dialect)
	require.NotEmpty(t, dsn)
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: dialect, DSN: dsn, Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	db := router.Write()
	raw, err := db.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(4)
	m, seed, ctx, detail := stageHarness(t)
	source := m.MigrationSource()
	source.Prefix = fmt.Sprintf("st_%x_", time.Now().UnixNano())
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, dialect))
	original := m.service.names
	m.service.names = query.BindNames(source.Prefix)
	var project model.Project
	require.NoError(t, seed.Table(original.Projects.String()).First(&project).Error)
	project.Stage = "DISCOVERY"
	require.NoError(t, query.BindProjects(db, m.service.names).WithContext(ctx).Create(&project))
	work, err := query.BindWorkItems(seed, original).WithContext(ctx).First()
	require.NoError(t, err)
	require.NoError(t, query.BindWorkItems(db, m.service.names).WithContext(ctx).Create(work))
	contract, err := query.BindWorkContracts(seed, original).WithContext(ctx).First()
	require.NoError(t, err)
	require.NoError(t, query.BindWorkContracts(db, m.service.names).WithContext(ctx).Create(contract))
	blocker := db.Begin()
	require.NoError(t, blocker.Error)
	defer blocker.Rollback()
	p := query.BindProjects(blocker, m.service.names)
	_, err = p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	require.NoError(t, err)
	w := query.BindWorkItems(blocker, m.service.names)
	_, err = w.WithContext(ctx).Where(w.ID.Eq(detail.ID)).UpdateSimple(w.State.Value("READY"), w.ReadinessOK.Value(true))
	require.NoError(t, err)
	reached := make(chan struct{})
	var reachedOnce sync.Once
	m.workPolicy = func(_ context.Context, tx *gorm.DB, _, _ guid.ID, _ string, _ guid.ID) (authz.DataConstraint, error) {
		var n int64
		e := tx.Table(m.service.names.Projects.String()).Count(&n).Error
		reachedOnce.Do(func() { close(reached) })
		return authz.DataConstraint{AllowAll: true}, e
	}
	result := make(chan error, 1)
	go func() {
		result <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			receipt, e := m.changeProjectStageOn(ctx, tx, 1, 99, 11, project.Version, stageChangeBody{ToStage: "PLANNING"}, nil)
			if e == nil && receipt.Project.Stage != "PLANNING" {
				return fmt.Errorf("unexpected stage %s", receipt.Project.Stage)
			}
			return e
		})
	}()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("command did not establish IAM snapshot")
	}
	require.NoError(t, blocker.Commit().Error)
	select {
	case err = <-result:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("command did not finish")
	}
}

func TestStageExecutorDoesNotTreatCapabilityFailuresAsEligibility(t *testing.T) {
	failure := errors.New("eligibility capability unavailable")
	for _, scenario := range []string{"member", "human", "agent", "missing_agent", "policy", "scope", "query"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := stageHarness(t)
			want := failure
			switch scenario {
			case "member":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, failure })
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			case "agent", "missing_agent":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				m.agents.ByPrincipalOn = nil
				want = ErrUnavailable
				if scenario == "agent" {
					m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
						return fleet.AgentIdentity{}, false, failure
					}
					want = failure
				}
			case "policy":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "scope":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
				want = ErrUnavailable
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:eligibility-query", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.WorkItems.String() {
						_ = tx.AddError(failure)
					}
				}))
			}
			eligible, err := m.stageExecutorOn(ctx, db, 1, 99, []guid.ID{11}, false)
			require.ErrorIs(t, err, want)
			require.False(t, eligible)
		})
	}
}

func TestProjectStagePrerequisiteQueriesFailClosed(t *testing.T) {
	for _, scenario := range []string{"source", "requirements", "confirmed_work", "criteria", "baseline", "ready", "constitution", "delivery"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := stageHarness(t)
			p := query.BindProjects(db, m.service.names)
			project, err := p.WithContext(ctx).First()
			require.NoError(t, err)
			project.Stage = "INTAKE"
			target := "DISCOVERY"
			failure := errors.New("required aggregate unavailable")
			table := m.service.names.Requirements.String()
			switch scenario {
			case "source":
				m.sources.ProjectFactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, *guid.ID) (intake.ProjectFacts, error) {
					return intake.ProjectFacts{}, failure
				}
			case "confirmed_work":
				table = m.service.names.WorkItems.String()
			case "criteria", "baseline":
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.createRequirementOn(ctx, tx, 1, 99, 11, requirementCreateBody{Title: "Registered requirement", Criteria: []requirementCriterionInput{{Text: "Required acceptance", Required: true}}})
					return e
				}))
				project.Stage, target = "DISCOVERY", "PLANNING"
				table = m.service.names.Criteria.String()
				if scenario == "baseline" {
					table = m.service.names.RequirementVersions.String()
				}
			case "ready":
				project.Stage, target, table = "PLANNING", "ACTIVE", m.service.names.WorkItems.String()
			case "constitution":
				project.Stage, target, table = "PLANNING", "ACTIVE", m.service.names.Constitutions.String()
				v := 1
				project.ConstitutionVersionNo = &v
			case "delivery":
				project.Stage, target, table = "ACTIVE", "ACCEPTANCE_PENDING", m.service.names.Deliveries.String()
			}
			if scenario != "source" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:prerequisite-query", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						_ = tx.AddError(failure)
					}
				}))
			}
			criteria, err := m.stageCriteriaOn(ctx, db, project, target, true)
			require.ErrorIs(t, err, failure)
			require.Nil(t, criteria, "unreadable facts must never be reported as satisfied guards")
		})
	}
}

func TestProjectStageMalformedCommandsNeverWrite(t *testing.T) {
	for _, scenario := range []string{"invalid_utf8", "long_reason", "invalid_version", "missing_authority", "missing_lock", "invalid_block_kind", "unexpected_block_kind"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := stageHarness(t)
			body := stageChangeBody{ToStage: "DISCOVERY"}
			version, want := 1, ErrInvalidArgument
			switch scenario {
			case "invalid_utf8":
				body.Reason = string([]byte{0xff})
			case "long_reason":
				body.Reason = strings.Repeat("a", 2001)
			case "invalid_version":
				version = 0
			case "missing_authority":
				m.stageAuthority, want = nil, ErrUnavailable
			case "missing_lock":
				m.principals.Lock, want = nil, ErrUnavailable
			case "invalid_block_kind":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "ACTIVE").Error)
				body.ToStage, body.Reason = "BLOCKED", "An explicit valid reason"
			case "unexpected_block_kind":
				kind := "EXTERNAL"
				body.BlockedKind = &kind
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.changeProjectStageOn(ctx, tx, 1, 99, 11, version, body, nil)
				return e
			})
			require.ErrorIs(t, err, want)
			row, err := query.BindProjects(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, 1, row.Version)
			count, err := query.BindStageTransitions(db, m.service.names).WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

func TestProjectStagePrincipalLocksAreSortedUniqueAndFailClosed(t *testing.T) {
	m, db, ctx, _ := stageHarness(t)
	var locked []guid.ID
	m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error {
		locked = append(locked, id)
		return nil
	}
	require.NoError(t, m.lockStagePrincipalsOn(ctx, db, 22, []guid.ID{22, 11, 11}))
	require.Equal(t, []guid.ID{11, 22}, locked, "all commands must acquire principal locks in the same order")
	locked = nil
	require.ErrorIs(t, m.lockStagePrincipalsOn(ctx, db, 22, []guid.ID{0, 11}), ErrInvalidArgument)
	require.Empty(t, locked)
	var absent *Module
	require.ErrorIs(t, absent.lockStagePrincipalsOn(ctx, db, 22, nil), ErrUnavailable)
}
