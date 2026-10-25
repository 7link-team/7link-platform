package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"os"
	"testing"
	"time"
)

func TestReviewPlanBindingRejectsChangedInputsWithSameCriterionIDs(t *testing.T) {
	for _, scenario := range []string{"verification", "criterion_text", "baseline_version", "contract", "unbound"} {
		t.Run(scenario, func(t *testing.T) {
			h, link := executionWorkFixture(t)
			w := query.BindWorkItems(h.db, h.m.service.names)
			c := query.BindWorkCriteria(h.db, h.m.service.names)
			p := query.BindReviewPlans(h.db, h.m.service.names)
			plan, e := p.WithContext(h.ctx).First()
			require.NoError(t, e)
			view, e := reviewPlanView(plan)
			require.NoError(t, e)
			require.NotNil(t, view.Content.Binding)
			require.NotEmpty(t, view.Content.Binding.ContractHash)
			switch scenario {
			case "verification":
				_, e = w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).Update(w.VerificationPlan, `[]`)
			case "criterion_text":
				_, e = c.WithContext(h.ctx).Where(c.WorkItemID.Eq(h.work)).Update(c.Text, "Changed acceptance requirement, same stable identifier")
			case "baseline_version":
				_, e = w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).Update(w.BaselineVersionNo, 2)
			case "contract":
				q := query.BindWorkContracts(h.db, h.m.service.names)
				_, e = q.WithContext(h.ctx).Where(q.WorkItemID.Eq(h.work)).Update(q.Snapshot, `{}`)
			case "unbound":
				view.Content.Binding = nil
				raw, err := json.Marshal(view.Content)
				require.NoError(t, err)
				_, e = p.WithContext(h.ctx).Where(p.ID.Eq(plan.ID)).Updates(map[string]any{"content": string(raw), "content_hash": planDigest(raw)})
			}
			require.NoError(t, e)
			before := h.snapshot()
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE") }), ErrPlanNotApproved)
			require.Equal(t, before, h.snapshot())
		})
	}
}

func TestReviewPlanBindingPinsRequirementBaselineAndHiddenSuite(t *testing.T) {
	for _, scenario := range []string{"baseline_hash", "suite_hash", "suite_tampering"} {
		t.Run(scenario, func(t *testing.T) {
			h, link := executionWorkFixture(t)
			next, err := h.m.ids.Next()
			require.NoError(t, err)
			requirementID := guid.ID(next)
			next, err = h.m.ids.Next()
			require.NoError(t, err)
			now := time.Now().UTC()
			requirement := &model.Requirement{TenantID: 1, ProjectID: 99, ID: requirementID, Number: 1, Title: "Frozen requirement", Description: "Requirement baseline", Priority: 1, Status: "BASELINED", CurrentVersionNo: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, query.BindRequirements(h.db, h.m.service.names).WithContext(h.ctx).Create(requirement))
			suite, err := json.Marshal(requirementFrozenSuite{RequirementID: requirementID, VersionNo: 1, Cases: []RequirementCaseSnapshot{{ID: guid.ID(next) + 1, Version: 1, WorkItemID: h.work, Title: "Hidden check", Steps: "Private executable acceptance content", Expected: "Private expected value", IsHiddenAcceptance: true}}})
			require.NoError(t, err)
			baseline := &model.RequirementVersion{TenantID: 1, ProjectID: 99, RequirementID: requirementID, ID: guid.ID(next), VersionNo: 1, Title: requirement.Title, Description: requirement.Description, Priority: 1, Criteria: "[]", ContentHash: planDigest([]byte(requirement.Description)), AcceptanceSuite: string(suite), SuiteHash: planDigest(suite), CreatedAt: now}
			v := query.BindRequirementVersions(h.db, h.m.service.names)
			require.NoError(t, v.WithContext(h.ctx).Create(baseline))
			w := query.BindWorkItems(h.db, h.m.service.names)
			_, err = w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).Updates(map[string]any{"requirement_id": requirementID, "baseline_version_no": 1})
			require.NoError(t, err)
			plan := approveFixtureReviewPlan(t, h)
			require.Equal(t, baseline.ContentHash, plan.Content.Binding.BaselineHash)
			require.Equal(t, baseline.SuiteHash, plan.Content.Binding.SuiteHash)
			exposed, err := json.Marshal(plan)
			require.NoError(t, err)
			require.NotContains(t, string(exposed), "Private executable acceptance content")
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE") }))
			switch scenario {
			case "baseline_hash":
				_, err = v.WithContext(h.ctx).Where(v.ID.Eq(baseline.ID)).Update(v.ContentHash, planDigest([]byte("changed baseline")))
			case "suite_hash":
				changed := append([]byte(" "), suite...)
				_, err = v.WithContext(h.ctx).Where(v.ID.Eq(baseline.ID)).Updates(map[string]any{"acceptance_suite": string(changed), "suite_hash": planDigest(changed)})
			case "suite_tampering":
				_, err = v.WithContext(h.ctx).Where(v.ID.Eq(baseline.ID)).Update(v.AcceptanceSuite, "{}")
			}
			require.NoError(t, err)
			before := h.snapshot()
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "DISPATCH") }), ErrPlanNotApproved)
			require.Equal(t, before, h.snapshot())
		})
	}
}

func TestReviewPlanCurrentReadOnRealMySQL8(t *testing.T) {
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
	source.Prefix = fmt.Sprintf("lp%d_", time.Now().UnixNano()%100000000)
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "mysql"))
	names := query.BindNames(source.Prefix)
	// Use an independently approved proposal produced by the actual domain
	// commands, not a synthetic runtime approval.
	h, _ := executionWorkFixture(t)
	projects, err := query.BindProjects(h.db, h.m.service.names).WithContext(h.ctx).Find()
	require.NoError(t, err)
	require.NoError(t, query.BindProjects(db, names).WithContext(h.ctx).Create(projects...))
	work := h.stored()
	require.NoError(t, query.BindWorkItems(db, names).WithContext(h.ctx).Create(work))
	plans, err := query.BindReviewPlans(h.db, h.m.service.names).WithContext(h.ctx).Find()
	require.NoError(t, err)
	contracts, err := query.BindWorkContracts(h.db, h.m.service.names).WithContext(h.ctx).Find()
	require.NoError(t, err)
	require.NoError(t, query.BindWorkContracts(db, names).WithContext(h.ctx).Create(contracts...))
	criteria, err := query.BindWorkCriteria(h.db, h.m.service.names).WithContext(h.ctx).Find()
	require.NoError(t, err)
	q := query.BindReviewPlans(db, names)
	require.NoError(t, q.WithContext(h.ctx).Create(plans...))
	require.NoError(t, query.BindWorkCriteria(db, names).WithContext(h.ctx).Create(criteria...))
	m := &Module{service: &projectService{names: names}}
	tx := db.Begin(&sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	snapshot := query.BindReviewPlans(tx, names)
	old, err := snapshot.WithContext(h.ctx).Order(snapshot.Round.Desc()).First()
	require.NoError(t, err)
	require.Equal(t, "APPROVED", old.State)
	newer := *old
	newer.ID++
	newer.Round++
	newer.State = "PROPOSED"
	newer.DecidedBy, newer.DecidedAt = nil, nil
	require.NoError(t, q.WithContext(h.ctx).Create(&newer))
	stillOld, err := snapshot.WithContext(h.ctx).Order(snapshot.Round.Desc()).First()
	require.NoError(t, err)
	require.Equal(t, old.ID, stillOld.ID, "the consistent-read snapshot must actually be stale")
	require.ErrorIs(t, m.requireApprovedReviewPlanOn(h.ctx, tx, work), ErrPlanNotApproved,
		"execution must observe the committed newer proposal despite its earlier snapshot")
	require.NoError(t, tx.Rollback().Error)
}

func TestReviewPlanCoverageImmutableDecisionSeparationAndCAS(t *testing.T) {
	m, db, ctx, work, _ := readinessHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	content := ReviewPlanContent{Files: []string{"src/feature.go"}, CriteriaCoverage: []PlanCoverage{}, Risks: []string{}, OutOfScopeConfirmed: true}
	for _, c := range work.Criteria {
		content.CriteriaCoverage = append(content.CriteriaCoverage, PlanCoverage{CriterionID: c.ID, Approach: "Implement exact criterion", Verification: "Run independent regression"})
	}
	var plan ReviewPlanView
	propose := func(c ReviewPlanContent) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var e error
			plan, e = m.proposeReviewPlanOn(ctx, tx, 1, 99, work.ID, 11, work.AccountablePrincipalID, work.Version, c)
			return e
		})
	}
	bad := content
	bad.CriteriaCoverage = nil
	require.ErrorIs(t, propose(bad), ErrResultCoverage)
	require.NoError(t, propose(content))
	require.Equal(t, "HUMAN", plan.SourceKind)
	require.Equal(t, "PROPOSED", plan.State)
	decide := func(c context.Context, actor guid.ID, version int, action, feedback string) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.decideReviewPlanOn(c, tx, 1, 99, work.ID, plan.ID, actor, work.AccountablePrincipalID, version, plan.ContentHash, action, feedback)
			return e
		})
	}
	require.ErrorIs(t, decide(ctx, 11, 1, "APPROVE", ""), ErrResultIndependent)
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	reviewer := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
	require.ErrorIs(t, decide(reviewer, 12, 1, "REJECT", ""), ErrInvalidArgument)
	require.NoError(t, decide(reviewer, 12, 1, "REJECT", "Add missing negative-path verification"))
	require.ErrorIs(t, decide(reviewer, 12, 1, "APPROVE", ""), ErrWorkVersion)
	q := query.BindReviewPlans(db, m.service.names)
	r, e := q.WithContext(ctx).Where(q.ID.Eq(plan.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "REJECTED", r.State)
	require.Equal(t, plan.ContentHash, r.ContentHash)
	require.EqualValues(t, 12, *r.DecidedBy)
}

func TestRequiredReviewPlanFencesImplementationExecution(t *testing.T) {
	h, link := executionWorkFixture(t)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id = ?", h.work).Update("plan_required", true).Error)
	require.NoError(t, h.db.Table(h.m.service.names.ReviewPlans.String()).Where("work_item_id = ?", h.work).Delete(nil).Error)
	before := h.snapshot()
	err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE") })
	require.ErrorIs(t, err, ErrResultState, "required plan approval must precede the execution fence")
	require.Equal(t, before, h.snapshot())
}

func TestRequiredReviewPlanRevalidatedAtEveryExecutionBoundary(t *testing.T) {
	for _, operation := range []string{"CREATE", "DISPATCH", "RECOVER", "START"} {
		for _, scenario := range []string{"approved", "missing", "pending", "rejected", "newer_pending", "stale_baseline", "changed_coverage", "tampered_content", "self_review", "missing_decision"} {
			t.Run(operation+"/"+scenario, func(t *testing.T) {
				h, link := executionWorkFixture(t)
				if operation != "CREATE" {
					require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE") }))
				}
				plans := h.db.Table(h.m.service.names.ReviewPlans.String()).Where("work_item_id = ?", h.work)
				switch scenario {
				case "missing":
					require.NoError(t, plans.Delete(nil).Error)
				case "pending":
					require.NoError(t, plans.Update("state", "PROPOSED").Error)
				case "rejected":
					require.NoError(t, plans.Update("state", "REJECTED").Error)
				case "newer_pending":
					q := query.BindReviewPlans(h.db, h.m.service.names)
					plan, err := q.WithContext(h.ctx).First()
					require.NoError(t, err)
					plan.ID++
					plan.Round++
					plan.State = "PROPOSED"
					plan.DecidedBy, plan.DecidedAt = nil, nil
					require.NoError(t, q.WithContext(h.ctx).Create(plan))
				case "stale_baseline":
					require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id = ?", h.work).Update("baseline_stale", true).Error)
				case "changed_coverage":
					require.NoError(t, h.db.Table(h.m.service.names.WorkCriteria.String()).Where("work_item_id = ?", h.work).Update("id", 987654321).Error)
				case "tampered_content":
					require.NoError(t, plans.Update("content", "{}").Error)
				case "self_review":
					require.NoError(t, plans.Update("decided_by", 11).Error)
				case "missing_decision":
					require.NoError(t, plans.Update("decided_at", nil).Error)
				}
				before := h.snapshot()
				err := h.db.Transaction(func(tx *gorm.DB) error {
					if operation == "START" {
						return h.m.startExecutionWorkOn(h.ctx, tx, link)
					}
					return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, operation)
				})
				if scenario == "approved" {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, ErrPlanNotApproved)
					require.Equal(t, before, h.snapshot(), "denied execution must leave work, audit and outbox unchanged")
				}
			})
		}
	}
}

func TestMissingReviewPlanDoesNotBlockReceiptOrCancellation(t *testing.T) {
	for _, operation := range []string{"RECEIPT", "CANCEL"} {
		t.Run(operation, func(t *testing.T) {
			h, link := executionWorkFixture(t)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE") }))
			policy := h.m.workPolicy
			h.m.workPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
				if permission == "execution_stop" && tenant == 1 && project == 99 && actor == 11 {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				return policy(ctx, tx, tenant, project, permission, actor)
			}
			require.NoError(t, h.db.Table(h.m.service.names.ReviewPlans.String()).Where("work_item_id = ?", h.work).Delete(nil).Error)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, operation) }))
		})
	}
}

func TestLowRiskPlanRequirementIsExplicit(t *testing.T) {
	for _, required := range []bool{false, true} {
		t.Run(map[bool]string{false: "optional", true: "required"}[required], func(t *testing.T) {
			h, link := executionWorkFixture(t)
			require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id = ?", h.work).Updates(map[string]any{"risk": "LOW", "autonomy_level": "LOW", "plan_required": required}).Error)
			require.NoError(t, h.db.Table(h.m.service.names.ReviewPlans.String()).Where("work_item_id = ?", h.work).Delete(nil).Error)
			before := h.snapshot()
			err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE") })
			if required {
				require.ErrorIs(t, err, ErrPlanNotApproved)
				require.Equal(t, before, h.snapshot())
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// Execution fixtures must satisfy the same independent plan prerequisite as
// real medium-risk work. This uses the actual proposal and decision commands.
func approveFixtureReviewPlan(t *testing.T, h *resultFixture) ReviewPlanView {
	t.Helper()
	policy := h.m.workPolicy
	h.m.workPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "plan_approve" && actor == 12 {
			return authz.DataConstraint{AllowAll: true}, nil
		}
		return policy(ctx, tx, tenant, project, permission, actor)
	}
	defer func() { h.m.workPolicy = policy }()
	work := h.stored()
	criteria, err := query.BindWorkCriteria(h.db, h.m.service.names).WithContext(h.ctx).Find()
	require.NoError(t, err)
	content := ReviewPlanContent{Files: []string{}, Risks: []string{}, OutOfScopeConfirmed: true}
	for _, criterion := range criteria {
		if criterion.WorkItemID == work.ID {
			content.CriteriaCoverage = append(content.CriteriaCoverage, PlanCoverage{CriterionID: criterion.ID, Approach: "Implement the frozen criterion", Verification: "Independent requirement verification"})
		}
	}
	var plan ReviewPlanView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		plan, e = h.m.proposeReviewPlanOn(h.ctx, tx, 1, 99, work.ID, 11, work.AccountablePrincipalID, work.Version, content)
		return e
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		plan, e = h.m.decideReviewPlanOn(h.actor(12), tx, 1, 99, work.ID, plan.ID, 12, work.AccountablePrincipalID, plan.Version, plan.ContentHash, "APPROVE", "Independent execution plan review")
		return e
	}))
	return plan
}

func TestReviewPlanProposalAndDecisionRollBackEveryFailedStorageStage(t *testing.T) {
	m, db, ctx, work, _ := readinessHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	content := ReviewPlanContent{Files: []string{"src/feature.go"}, Risks: []string{}, OutOfScopeConfirmed: true}
	for _, c := range work.Criteria {
		content.CriteriaCoverage = append(content.CriteriaCoverage, PlanCoverage{CriterionID: c.ID, Approach: "Implement frozen criterion", Verification: "Independent negative and success paths"})
	}
	var plan ReviewPlanView
	propose := func(tx *gorm.DB) error {
		var e error
		plan, e = m.proposeReviewPlanOn(ctx, tx, 1, 99, work.ID, 11, work.AccountablePrincipalID, work.Version, content)
		return e
	}
	failure := errors.New("plan persistence unavailable")
	rollback := errors.New("rollback successful plan probe")
	for _, operation := range []string{"propose", "decide"} {
		if operation == "decide" {
			require.NoError(t, db.Transaction(propose))
		}
		var beforeCount int64
		require.NoError(t, db.Table(m.service.names.ReviewPlans.String()).Count(&beforeCount).Error)
		calls, failAt := 0, 0
		probe := func(tx *gorm.DB) {
			calls++
			if calls == failAt {
				tx.AddError(failure)
			}
		}
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("plan-current-failure", probe))
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("plan-current-failure", probe))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("plan-current-failure", probe))
		execute := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				var e error
				if operation == "propose" {
					e = propose(tx)
				} else {
					actor := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
					_, e = m.decideReviewPlanOn(actor, tx, 1, 99, work.ID, plan.ID, 12, work.AccountablePrincipalID, plan.Version, plan.ContentHash, "APPROVE", "Reviewed exact current sources")
				}
				if e != nil {
					return e
				}
				return rollback
			})
		}
		require.ErrorIs(t, execute(), rollback)
		total := calls
		require.Greater(t, total, 3)
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, execute(), failure, "%s storage step %d", operation, i)
		}
		require.NoError(t, db.Callback().Query().Remove("plan-current-failure"))
		require.NoError(t, db.Callback().Create().Remove("plan-current-failure"))
		require.NoError(t, db.Callback().Update().Remove("plan-current-failure"))
		var count int64
		require.NoError(t, db.Table(m.service.names.ReviewPlans.String()).Count(&count).Error)
		require.Equal(t, beforeCount, count)
		if operation == "decide" {
			stored, e := query.BindReviewPlans(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, "PROPOSED", stored.State)
			require.Nil(t, stored.DecidedAt)
		}
	}
}
