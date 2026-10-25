package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestProjectMetricsLocksPrincipalBeforeProjectAuthorization(t *testing.T) {
	h := newResultFixture(t)
	locked := false
	h.m.principals.Lock = func(_ context.Context, _ *gorm.DB, actor guid.ID) error {
		require.EqualValues(t, 12, actor)
		locked = true
		return nil
	}
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		require.True(t, locked, "project authorization must follow the principal lock")
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	start := time.Now().UTC()
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.projectMetricOn(h.actor(12), tx, 1, 99, start, start.Add(time.Hour))
		return err
	}), ErrWorkForbidden)
	require.True(t, locked)
}

func TestProjectMetricsPublicRoutePreservesCurrentCohortAndSourceFailures(t *testing.T) {
	h := newResultFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	registry, e := authz.NewRegistry(authz.Action{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true})
	require.NoError(t, e)
	guard := resultHTTPGuard{ctx: h.ctx}
	h.m.registrar = authz.NewRegistrar(registry, guard, guard, guard)
	_, api := humatest.New(t)
	h.m.registerProjectMetricsHTTP(api)
	for _, suffix := range []string{"?project_id=99", ""} {
		r := api.Get("/tenants/1/metrics/projects" + suffix)
		require.Equal(t, 200, r.Code, r.Body.String())
		var body struct {
			Data ProjectMetrics `json:"data"`
		}
		require.NoError(t, json.Unmarshal(r.Body.Bytes(), &body))
		require.Len(t, body.Data.Projects, 1)
		require.Equal(t, guid.ID(99), body.Data.Projects[0].ProjectID)
		require.Equal(t, 1, body.Data.Team.WorkItems)
	}
	for _, query := range []string{"from=2026-02-30", "to=2026-02-30", "from=2026-01-01&to=2026-10-01", "from=2026-10-01&to=2026-10-01", "project_id=9999999999999999999"} {
		r := api.Get("/tenants/1/metrics/projects?" + query)
		require.Equal(t, 422, r.Code, r.Body.String())
	}
	h.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return errors.New("principal unavailable") }
	require.Equal(t, 500, api.Get("/tenants/1/metrics/projects?project_id=99").Code)
	h.m.runtime = nil
	require.Equal(t, 503, api.Get("/tenants/1/metrics/projects").Code)
}

func TestMetricSourceConnectorsBindActualReadersOnlyOnce(t *testing.T) {
	h := newResultFixture(t)
	s := module.NewServices()
	require.NoError(t, h.m.provideProjectMetrics(s))
	require.NoError(t, s.Seal())
	c, e := module.Resolve[DeliveryMetricsSourcesConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, c.BindExecutions(nil), ErrInvalidArgument)
	require.ErrorIs(t, c.BindClient(nil), ErrInvalidArgument)
	h.m.metricsSources = DeliveryMetricsSources{}
	require.NoError(t, c.BindExecutions(func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]MetricExecution, error) {
		return []MetricExecution{{ID: 900, WorkItemID: h.work, Model: "configured"}}, nil
	}))
	require.NoError(t, c.BindClient(func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) (MetricClientHistory, error) {
		return MetricClientHistory{Complete: false}, nil
	}))
	require.ErrorIs(t, c.BindExecutions(h.m.metricsSources.Executions), ErrInvalidArgument)
	require.ErrorIs(t, c.BindClient(h.m.metricsSources.Client), ErrInvalidArgument)
}

func TestProjectMetricsVerifierCohortUsesFrozenRoundsAndExplicitEscapes(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	start := time.Now().UTC().Truncate(24 * time.Hour)
	at := start.Add(time.Hour)
	end := start.Add(24 * time.Hour)
	require.NoError(t, h.db.Table(h.m.service.names.ReportQualityObservations.String()).Where("singleton=?", 1).Update("observed_since", start.Add(-time.Hour)).Error)
	criterion := guid.ID(31)
	plan, _ := json.Marshal([]workVerificationStep{{CriterionID: criterion, Method: "COMMAND", Detail: "Run command"}})
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", result.ID).Update("verification_plan", string(plan)).Error)
	for i, stage := range []string{"AUTOMATED", "AGENT_TEST", "AGENT_ACCEPTANCE"} {
		done := at.Add(time.Minute)
		r := model.VerificationRun{TenantID: 1, ID: guid.ID(800 + i), ProjectID: 99, WorkItemID: h.work, ResultID: result.ID, CriterionID: criterion, Method: "COMMAND", Round: 1, Stage: stage, State: "DONE", Verdict: "PASSED", ReceiptHash: strings.Repeat("a", 64), ReviewerModel: "configured-reviewer", CreatedAt: at, CompletedAt: &done}
		require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Create(&r).Error)
	}
	for i, kind := range []string{"INTERNAL_QA_PASSED", "L3_ENTERED", "ESCAPE_HUMAN", "ESCAPE_CLIENT"} {
		rounds := 0
		if kind == "INTERNAL_QA_PASSED" {
			rounds = 1
		}
		r := model.ReportQualityReceipt{TenantID: 1, ID: guid.ID(900 + i), ProjectID: 99, WorkItemID: h.work, SourceID: guid.ID(900 + i), Kind: kind, Rounds: rounds, OccurredAt: at.Add(time.Duration(i+2) * time.Minute)}
		require.NoError(t, h.db.Table(h.m.service.names.ReportQualityReceipts.String()).Create(&r).Error)
	}
	h.m.metricsSources.Client = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) (MetricClientHistory, error) {
		return MetricClientHistory{Complete: true, Receipts: []MetricClientReceipt{{PackageID: 1000, Package: true, Accepted: true, At: at.Add(time.Hour)}, {PackageID: 1000, WorkItemID: h.work, ItemKey: "item", Accepted: true, At: at.Add(time.Hour)}}}, nil
	}
	read := func(selection MetricSelection) (out ProjectMetricSnapshot, err error) {
		err = h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = h.m.projectMetricSelectedOn(h.ctx, tx, 1, 99, start, end, selection)
			return e
		})
		return
	}
	out, e := read(MetricSelection{Verifier: "configured-reviewer"})
	require.NoError(t, e)
	require.Equal(t, 1, out.WorkItems)
	require.Equal(t, "1.000000", *out.KPIs["L2_FIRST_PASS"].Value)
	require.Equal(t, "1.000000", *out.KPIs["L3_ESCAPE"].Value)
	require.Equal(t, "1.000000", *out.KPIs["L4_ESCAPE"].Value)
	require.Equal(t, "1", *out.InternalRounds.P95)
	require.Equal(t, []string{"configured-reviewer"}, out.Versions["verifier"])
	empty, e := read(MetricSelection{Verifier: "other-reviewer"})
	require.NoError(t, e)
	require.Zero(t, empty.WorkItems)
	h.m.metricsSources.Client = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) (MetricClientHistory, error) {
		return MetricClientHistory{Complete: false}, nil
	}
	out, e = read(MetricSelection{})
	require.NoError(t, e)
	for _, key := range []string{"L4_PACKAGE_FIRST_ACCEPTANCE", "L4_ITEM_FIRST_ACCEPTANCE", "L4_ESCAPE"} {
		require.Nil(t, out.KPIs[key].Value)
		require.Equal(t, "INCOMPLETE_CLIENT_HISTORY", out.KPIs[key].Reason)
	}
	h.m.metricsSources.Executions = nil
	_, e = read(MetricSelection{Model: "unavailable"})
	require.ErrorIs(t, e, ErrUnavailable)
	injected := errors.New("execution cohort unavailable")
	h.m.metricsSources.Executions = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]MetricExecution, error) {
		return nil, injected
	}
	_, e = read(MetricSelection{Model: "configured"})
	require.ErrorIs(t, e, injected)
}

func TestProjectMetricsStorageFailuresCannotBecomeSuccessfulZeroSamples(t *testing.T) {
	h := newResultFixture(t)
	_, e := h.submit()
	require.NoError(t, e)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	start := time.Now().UTC().Truncate(24 * time.Hour)
	injected := errors.New("metric source unavailable")
	queries, failAt := 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("metric-source-failure", func(tx *gorm.DB) {
		queries++
		if queries == failAt {
			tx.AddError(injected)
		}
	}))
	defer h.db.Callback().Query().Remove("metric-source-failure")
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.projectMetricOn(h.ctx, tx, 1, 99, start, start.Add(24*time.Hour))
			return e
		})
	}
	require.NoError(t, read())
	total := queries
	require.Greater(t, total, 5)
	for i := 1; i <= total; i++ {
		queries, failAt = 0, i
		require.ErrorIs(t, read(), injected, "failed historical read %d of %d must remain explicit", i, total)
	}
}

func TestProjectMetricsExactWindowCohortFirstDecisionCostsAndMissingLabor(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	originalPolicy := h.m.workPolicy
	h.m.workPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "project_read" {
			permission = "work_item_read"
		}
		return originalPolicy(ctx, tx, tenant, project, permission, actor)
	}
	start := time.Now().UTC().Truncate(24 * time.Hour)
	end := start.Add(48 * time.Hour)
	at := start.Add(12 * time.Hour)
	require.NoError(t, h.db.Table(h.m.service.names.ReportQualityObservations.String()).Where("singleton = ?", 1).Update("observed_since", start.Add(-48*time.Hour)).Error)
	minute := int64(60000)
	for i, row := range []model.ResultAction{
		{Kind: "RETURN", Verdict: "FAILED", CreatedAt: at},
		{Kind: "ACCEPT", Verdict: "PASSED", AttentionMS: &minute, CreatedAt: at.Add(time.Hour)},
		{Kind: "RECEIPT", Verdict: "PASSED", CreatedAt: at.Add(2 * time.Hour)},
	} {
		row.TenantID = 1
		row.ID = guid.ID(810 + i)
		row.ProjectID = 99
		row.WorkItemID = h.work
		row.ResultID = result.ID
		row.ActorID = 12
		row.AuthoritySource = "permission:decision_approve"
		row.Reason = "Frozen actual decision"
		require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Create(&row).Error)
	}
	ended := at.Add(time.Hour)
	attention := model.AttentionSession{TenantID: 1, ID: 950, ProjectID: 99, WorkItemID: h.work, ClaimID: &result.ID, PrincipalID: 12, Surface: "REVIEW_DESK", StartedAt: at, LastHeartbeatAt: ended, EndedAt: &ended, ActiveMS: 60000, LastSequence: 1}
	require.NoError(t, h.db.Table(h.m.service.names.AttentionSessions.String()).Create(&attention).Error)
	for i, state := range []string{"REWORK", "ACCEPTED"} {
		row := model.WorkItemTransition{TenantID: 1, ID: guid.ID(920 + i), ProjectID: 99, WorkItemID: h.work, ToState: state, Trigger: "COMMAND", ActorKind: "HUMAN", AuthoritySource: "permission:decision_approve", ReasonCode: "RESULT_ACCEPTED", VersionAfter: i + 2, CreatedAt: at.Add(time.Duration(i) * time.Hour)}
		require.NoError(t, h.db.Table(h.m.service.names.WorkTransitions.String()).Create(&row).Error)
	}
	h.m.metricsSources.Executions = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, ids []guid.ID, from, to time.Time) ([]MetricExecution, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.Equal(t, []guid.ID{h.work}, ids)
		require.Equal(t, start, from)
		require.Equal(t, end, to)
		return []MetricExecution{{ID: 1, WorkItemID: h.work, Model: "observed-model", WorkflowVersion: "42:hash"}, {ID: 2, WorkItemID: h.work}}, nil
	}
	h.m.acceptanceSettlements = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, ids []guid.ID) (int64, bool, error) {
		if ids[0] == 1 {
			return 9007199254740993, true, nil
		}
		return 0, false, nil
	}
	h.m.metricsSources.Client = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) (MetricClientHistory, error) {
		return MetricClientHistory{Complete: true, Receipts: []MetricClientReceipt{{PackageID: 50, At: at, Package: true}, {PackageID: 50, WorkItemID: h.work, ItemKey: "50:a", At: at, Accepted: true}, {PackageID: 50, WorkItemID: h.work, ItemKey: "50:b", At: at}}}, nil
	}
	var out ProjectMetricSnapshot
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = h.m.projectMetricOn(h.actor(12), tx, 1, 99, start, end)
		return e
	}))
	require.Equal(t, "0.000000", *out.KPIs["L3_FIRST_ACCEPTANCE"].Value)
	require.Equal(t, "1", *out.KPIs["L3_FIRST_ACCEPTANCE"].Denominator)
	cost := out.KPIs["KNOWN_COST_LOWER_BOUND_MICROS"]
	require.Equal(t, "9007199254740993", *cost.Numerator)
	require.Equal(t, "9007199254740993.000000", *cost.Value)
	require.Equal(t, "0.500000", *cost.MissingRate)
	require.True(t, cost.LowerBound)
	require.Nil(t, out.KPIs["COST_PER_ACCEPTED_MICROS"].Value)
	require.Equal(t, "1.000000", *out.KPIs["ATTENTION_MIN_PER_ACCEPTED"].Value)
	require.Equal(t, "0.500000", *out.KPIs["HUMAN_TIME_MISSING"].Value)
	require.Equal(t, "0.500000", *out.KPIs["L4_ITEM_FIRST_ACCEPTANCE"].Value)
	require.Equal(t, "2", *out.KPIs["L4_ITEM_FIRST_ACCEPTANCE"].Denominator)
	require.Equal(t, "0.000000", *out.KPIs["L4_PACKAGE_FIRST_ACCEPTANCE"].Value)
	require.Equal(t, "3600000", *out.ReworkCycle.P95)
	require.Equal(t, 1, out.ReworkCycle.Samples)
	require.Contains(t, out.MissingSources, "LABOR_PRICING")
	h.m.metricsSources.Executions = nil
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		empty, e := h.m.projectMetricOn(h.actor(12), tx, 1, 99, end, end.Add(time.Hour))
		require.Nil(t, empty.KPIs["L3_FIRST_ACCEPTANCE"].Value)
		require.Nil(t, empty.KPIs["EVIDENCE_COVERAGE"].Value)
		return e
	}))
}

func TestProjectMetricsCurrentAuthorityAndSourceFailuresNeverProduceZeros(t *testing.T) {
	h := newResultFixture(t)
	_, err := h.submit()
	require.NoError(t, err)
	start := time.Now().Add(-time.Hour)
	end := time.Now().Add(time.Hour)
	originalPolicy := h.m.workPolicy
	denied := errors.New("current access revoked")
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, denied
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.projectMetricOn(h.actor(12), tx, 1, 99, start, end); return e }), denied)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.projectMetricOn(h.actor(12), tx, 2, 99, start, end); return e }))
	h.m.workPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "project_read" {
			permission = "work_item_read"
		}
		return originalPolicy(ctx, tx, tenant, project, permission, actor)
	}
	sourceFailure := errors.New("receipt owner storage unavailable")
	h.m.metricsSources.Client = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) (MetricClientHistory, error) {
		return MetricClientHistory{}, sourceFailure
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.projectMetricOn(h.actor(12), tx, 1, 99, start.UTC(), end.UTC())
		return e
	}), sourceFailure)

}

func TestProjectMetricsFirstQACannotHideFailedRoundBranchOrManualOverride(t *testing.T) {
	at := time.Now().UTC()
	plan := `[{"criterion_id":"3","method":"COMMAND","detail":"Frozen command"}]`
	results := []*model.ResultVersion{{ID: 1, WorkItemID: 9, VerificationPlan: plan}}
	runs := []*model.VerificationRun{}
	for i, stage := range []string{"AUTOMATED", "AGENT_TEST", "AGENT_ACCEPTANCE"} {
		runs = append(runs, &model.VerificationRun{ID: guid.ID(i + 1), WorkItemID: 9, ResultID: 1, CriterionID: 3, Method: "COMMAND", Stage: stage, Round: 1, State: "DONE", Verdict: "PASSED", ReceiptHash: strings.Repeat("a", 64), CreatedAt: at, CompletedAt: &at})
	}
	require.True(t, metricFirstQAPassed(9, runs, results, at))
	runs[0].Verdict = "FAILED"
	require.False(t, metricFirstQAPassed(9, runs, results, at), "overrides must not rewrite original failed attempt")
	runs[0].Verdict = "PASSED"
	runs[0].Round = 2
	require.False(t, metricFirstQAPassed(9, runs, results, at))
	runs[0].Round = 1
	runs[0].ResultID = 2
	require.False(t, metricFirstQAPassed(9, runs, results, at), "a later claim cannot become the first branch")
	runs[0].ResultID = 1
	runs[0].CompletedAt = nil
	require.False(t, metricFirstQAPassed(9, runs, results, at))
}

func TestProjectMetricsQuantilesAreNearestRankAndTeamRatiosUseDenominators(t *testing.T) {
	d := metricDistribution([]int64{26, 2, 5, 3}, "hours")
	require.Equal(t, "3", *d.P50)
	require.Equal(t, "26", *d.P90)
	require.Equal(t, "9.000000", *d.Mean)
	require.Nil(t, metricDistribution(nil, "ms").P95)
	a := ProjectMetricSnapshot{WorkItems: 1, KPIs: map[string]MetricRatio{"L3_FIRST_ACCEPTANCE": metricRatio(1, 1)}, roundSamples: []int64{1}, Versions: map[string][]string{}, MissingSources: []string{}}
	b := ProjectMetricSnapshot{WorkItems: 9, KPIs: map[string]MetricRatio{"L3_FIRST_ACCEPTANCE": metricRatio(0, 9)}, roundSamples: []int64{9}, Versions: map[string][]string{}, MissingSources: []string{}}
	team := metricTeam([]ProjectMetricSnapshot{a, b})
	require.Equal(t, "0.100000", *team.KPIs["L3_FIRST_ACCEPTANCE"].Value)
	require.Equal(t, "1", *team.InternalRounds.P50)
	require.Equal(t, "9", *team.InternalRounds.P95)
}

func TestProjectMetricsVersionSelectionFiltersAuthorizedCohortBeforeAggregation(t *testing.T) {
	h := newResultFixture(t)
	_, err := h.submit()
	require.NoError(t, err)
	original := h.m.workPolicy
	h.m.workPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "project_read" {
			permission = "work_item_read"
		}
		return original(ctx, tx, tenant, project, permission, actor)
	}
	h.m.metricsSources.Executions = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]MetricExecution, error) {
		return []MetricExecution{{ID: 1, WorkItemID: h.work, Model: "observed-v2", WorkflowVersion: "42:hash"}}, nil
	}
	h.m.acceptanceSettlements = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) { return 7, true, nil }
	start, end := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		selected, e := h.m.projectMetricSelectedOn(h.actor(12), tx, 1, 99, start, end, MetricSelection{Model: "observed-v2", Workflow: "42:hash"})
		require.NoError(t, e)
		require.Equal(t, 1, selected.WorkItems)
		require.Equal(t, []string{"observed-v2"}, selected.Versions["model"])
		empty, e := h.m.projectMetricSelectedOn(h.actor(12), tx, 1, 99, start, end, MetricSelection{Model: "other-v3"})
		require.NoError(t, e)
		require.Zero(t, empty.WorkItems)
		require.Nil(t, empty.KPIs["L3_FIRST_ACCEPTANCE"].Value)
		require.Nil(t, empty.InternalRounds.P95)
		return nil
	}))
}

func TestProjectMetricsTeamPreservesUnknownZeroDenominatorAcrossProjects(t *testing.T) {
	unknown := metricRatio(0, 0)
	unknown.Reason = "INCOMPLETE_CLIENT_HISTORY"
	known := metricRatio(1, 1)
	project := func(metric MetricRatio) ProjectMetricSnapshot {
		return ProjectMetricSnapshot{KPIs: map[string]MetricRatio{"L4_FIRST_ACCEPTANCE": metric}, Versions: map[string][]string{}}
	}
	for _, projects := range [][]ProjectMetricSnapshot{
		{project(unknown), project(known), project(known)},
		{project(known), project(unknown), project(known)},
		{project(known), project(known), project(unknown)},
	} {
		metric := metricTeam(projects).KPIs["L4_FIRST_ACCEPTANCE"]
		require.Nil(t, metric.Value, "an unknown cohort must not become a complete ratio")
		require.Equal(t, "INCOMPLETE_CLIENT_HISTORY", metric.Reason)
		require.Equal(t, "2", *metric.Denominator)
	}
	metric := metricTeam([]ProjectMetricSnapshot{project(metricRatio(0, 0)), project(known)}).KPIs["L4_FIRST_ACCEPTANCE"]
	require.Equal(t, "1.000000", *metric.Value, "known empty cohorts do not invalidate an observed ratio")
	for _, reason := range []string{"MEASURED_REVIEW_ONLY", "EXCLUDES_UNPRICED_LABOR_MACHINE_PLATFORM"} {
		empty := metricMissing(metricRatio(0, 0), 0, 0, reason, true)
		metric = metricTeam([]ProjectMetricSnapshot{project(empty), project(known)}).KPIs["L4_FIRST_ACCEPTANCE"]
		require.Equal(t, "1.000000", *metric.Value, "informational lower-bound labels are not unknown source facts")
	}
}

func TestProjectMetricsCountOnlyDistinctVisibleChangedWorkAndFailClosedOnImpactReads(t *testing.T) {
	h := newResultFixture(t)
	_, e := h.submit()
	require.NoError(t, e)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	start := time.Now().UTC().Add(-time.Hour)
	end := start.Add(2 * time.Hour)
	require.NoError(t, h.db.Table(h.m.service.names.Requirements.String()).Create(&model.Requirement{TenantID: 1, ID: 31, ProjectID: 99, Number: 1, Title: "Scoped requirement", Description: "Actual scope boundary", Priority: 3, Status: "DRAFT", CurrentVersionNo: 1, Version: 1, CreatedAt: start, UpdatedAt: start}).Error)
	for i := 0; i < 2; i++ {
		change := model.ChangeRecord{TenantID: 1, ID: guid.ID(990 + i), ProjectID: 99, RequirementID: 31, Kind: "SCOPE", FromVersionNo: i + 1, ToVersionNo: i + 2, Summary: "Changed criterion", Diff: "{}", AuthoritySource: "permission:requirement_write", ActorID: 11, CreatedAt: start.Add(time.Minute)}
		require.NoError(t, h.db.Table(h.m.service.names.Changes.String()).Create(&change).Error)
		impact := model.ChangeImpact{TenantID: 1, ChangeRecordID: change.ID, WorkItemID: h.work, ProjectID: 99, Impact: "STALE", VersionBefore: 1}
		require.NoError(t, h.db.Table(h.m.service.names.ChangeImpacts.String()).Create(&impact).Error)
	}
	read := func(selection MetricSelection) (ProjectMetricSnapshot, error) {
		var out ProjectMetricSnapshot
		e := h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = h.m.projectMetricSelectedOn(h.ctx, tx, 1, 99, start, end, selection)
			return e
		})
		return out, e
	}
	out, e := read(MetricSelection{})
	require.NoError(t, e)
	require.Equal(t, 1, out.ScopeChanged)
	failure := errors.New("current metric impact unavailable")
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("metric-impacts-unavailable", func(tx *gorm.DB) {
		if tx.Statement.Table == h.m.service.names.ChangeImpacts.String() {
			tx.AddError(failure)
		}
	}))
	_, e = read(MetricSelection{})
	require.ErrorIs(t, e, failure)
	require.NoError(t, h.db.Callback().Query().Remove("metric-impacts-unavailable"))
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("metric-verifier-unavailable", func(tx *gorm.DB) {
		if tx.Statement.Table == h.m.service.names.VerificationRuns.String() {
			tx.AddError(failure)
		}
	}))
	_, e = read(MetricSelection{Verifier: "current-reviewer"})
	require.ErrorIs(t, e, failure)
	require.NoError(t, h.db.Callback().Query().Remove("metric-verifier-unavailable"))
}

func TestProjectMetricVerifierCohortUsesCurrentPinnedReviewerModel(t *testing.T) {
	h, result, in := verificationFixture(t)
	_, e := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, result.ID, 12, 11, v, in)
	})
	require.NoError(t, e)
	require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Where("work_item_id=?", h.work).Update("reviewer_model", "observed-reviewer").Error)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	start, end := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour)
	read := func(selection MetricSelection) (ProjectMetricSnapshot, error) {
		var out ProjectMetricSnapshot
		e := h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = h.m.projectMetricSelectedOn(h.ctx, tx, 1, 99, start, end, selection)
			return e
		})
		return out, e
	}
	out, e := read(MetricSelection{Verifier: "observed-reviewer"})
	require.NoError(t, e)
	require.Equal(t, 1, out.WorkItems)
	out, e = read(MetricSelection{Verifier: "other-reviewer"})
	require.NoError(t, e)
	require.Zero(t, out.WorkItems)
	_, e = read(MetricSelection{Model: "actual-model"})
	require.ErrorIs(t, e, ErrUnavailable)
	failure := errors.New("execution model source unavailable")
	h.m.metricsSources.Executions = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]MetricExecution, error) {
		return nil, failure
	}
	_, e = read(MetricSelection{Model: "actual-model"})
	require.ErrorIs(t, e, failure)
}

func TestProjectMetricStorageFailuresCannotProduceCompleteHistoricalSnapshot(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	_, e = h.accept(result.ID, 13)
	require.NoError(t, e)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	failure := errors.New("historical metric storage unavailable")
	calls, failAt := 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("metric-storage-failure", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			out, e := h.m.projectMetricSelectedOn(h.ctx, tx, 1, 99, time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour), MetricSelection{})
			if e == nil {
				require.Equal(t, 1, out.WorkItems)
			}
			return e
		})
	}
	require.NoError(t, read())
	total := calls
	require.Greater(t, total, 10)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, read(), failure, "historical metric stage %d", i)
	}
	require.NoError(t, h.db.Callback().Query().Remove("metric-storage-failure"))
}
