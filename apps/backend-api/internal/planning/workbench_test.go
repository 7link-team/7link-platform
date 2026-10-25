package planning

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkbenchCurrentTeamNamesAndReportActivityRespectSourceScope(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
	h.m.principals.ActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id, DisplayName: "Current member"}, id != 8, nil
	}
	h.facts.Work = append(h.facts.Work, delivery.PlanningWorkFact{ID: 102, State: "BLOCKED", AccountablePrincipalID: 9})
	confirmed := h.m.now().Add(-time.Hour)
	outside := h.m.now().AddDate(0, 0, -30)
	for i, source := range []struct {
		tenant, project, principal guid.ID
		at                         time.Time
	}{
		{1, 3, 2, confirmed}, {1, 3, 3, confirmed}, {2, 3, 2, confirmed}, {1, 4, 2, confirmed}, {1, 3, 2, outside},
	} {
		row := model.Report{TenantID: source.tenant, ID: guid.ID(500 + i), ProjectID: source.project, PrincipalID: source.principal, PeriodKind: "WEEK", PeriodStart: source.at.UTC().Format(time.DateOnly), State: "CONFIRMED", Content: "{}", ContentHash: "source", Facts: "{}", Version: 1, CreatedBy: source.principal, ConfirmedBy: &source.principal, ConfirmedAt: &source.at}
		require.NoError(t, h.db.Table(h.m.names.Reports.String()).Create(&row).Error)
	}
	policy := h.m.entityPolicy
	h.m.entityPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "report_read" {
			return authz.DataConstraint{OwnerIDs: []string{actor.String()}}, nil
		}
		return policy(ctx, tx, tenant, project, permission, actor)
	}
	out, err := workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	require.Len(t, out.Activity, 2, "only authorized current report confirmation and visible work acceptance")
	require.Equal(t, "REPORT_CONFIRMED", out.Activity[0].Kind)
	require.EqualValues(t, 500, out.Activity[0].ObjectID)
	require.Equal(t, confirmed, out.Activity[0].OccurredAt)
	for _, load := range out.Load {
		if load.PrincipalID == 2 {
			require.Equal(t, "Current member", load.DisplayName)
		} else {
			require.Empty(t, load.DisplayName, "inactive tenant member must not expose identity metadata")
		}
	}
	failure := errors.New("identity source failed")
	h.m.principals.ActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, failure
	}
	_, err = workbenchRead(t, h, 2, "", "")
	require.ErrorIs(t, err, failure)
}

func workbenchRead(t *testing.T, h harness, actor guid.ID, from, to string) (WorkbenchSummary, error) {
	t.Helper()
	var out WorkbenchSummary
	err := h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = h.m.workbenchOn(claims(t, actor), tx, 1, from, to)
		return err
	})
	return out, err
}

func TestWorkbenchUsesAuthorizedActualPeriodFactsAndCurrentLoad(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
	end := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, 0, -8)
	h.facts.Work = append(h.facts.Work,
		delivery.PlanningWorkFact{ID: 102, State: "BLOCKED", AccountablePrincipalID: 3},
		delivery.PlanningWorkFact{ID: 103, State: "NEEDS_HUMAN_REVIEW", AccountablePrincipalID: 3},
		delivery.PlanningWorkFact{ID: 104, State: goalCancelled, AccountablePrincipalID: 3},
		delivery.PlanningWorkFact{ID: 105, State: "ACCEPTED", AcceptedAt: &end},
		delivery.PlanningWorkFact{ID: 106, State: "ACCEPTED", AcceptedAt: &start},
	)
	h.m.executions.ReadOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, ids []guid.ID, from, to time.Time) (execution.PeriodFacts, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		require.Len(t, ids, 7)
		require.Equal(t, start, from)
		require.Equal(t, end, to)
		return execution.PeriodFacts{Created: 4}, nil
	}
	h.m.runtimeCosts.PeriodOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (int64, bool, error) {
		return 840000, true, nil
	}
	out, err := workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	require.Equal(t, 1, out.Projects)
	require.Equal(t, 7, out.WorkItems)
	require.Equal(t, 3, out.Open)
	require.Equal(t, 1, out.Blocked)
	require.Equal(t, 1, out.Review)
	require.Equal(t, 2, out.Accepted)
	require.Equal(t, int64(4), *out.ExecutionCreated)
	require.Equal(t, "840000", *out.ExecutionCostMicros)
	require.Equal(t, []WorkbenchLoad{{PrincipalID: 3, Open: 2, Blocked: 1, Review: 1}, {PrincipalID: 2, Open: 1}}, out.Load)
	require.Equal(t, guid.ID(100), out.Acceptances[0].WorkItemID)
	require.Equal(t, guid.ID(106), out.Acceptances[1].WorkItemID)
	require.Equal(t, []string{"CLIENT_ACCEPTANCE"}, out.MissingSources)
}

func TestWorkbenchDeniesBeforeSourceReadsAndNeverTurnsFailureIntoEmptyData(t *testing.T) {
	h := setup(t)
	reads := 0
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { reads++; return []guid.ID{3}, nil }
	for _, actor := range []guid.ID{7, 8, 9} {
		_, err := workbenchRead(t, h, actor, "", "")
		require.Error(t, err)
	}
	require.Zero(t, reads)
	for _, dates := range [][2]string{{"2026-02-30", ""}, {"", "bad"}, {"2026-10-20", "2026-10-20"}, {"2026-01-01", "2026-10-20"}} {
		_, err := workbenchRead(t, h, 2, dates[0], dates[1])
		require.ErrorIs(t, err, ErrInvalid)
	}
	require.Zero(t, reads)
	_, err := h.m.workbenchOn(claims(t, 2), h.db, 1, "", "")
	require.ErrorIs(t, err, ErrInvalid)
	failure := errors.New("source unavailable")
	h.m.facts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
		return delivery.PlanningSnapshot{}, failure
	}
	_, err = workbenchRead(t, h, 2, "", "")
	require.ErrorIs(t, err, failure)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return nil, failure }
	_, err = workbenchRead(t, h, 2, "", "")
	require.ErrorIs(t, err, failure)
	h.m.facts.ProjectsOn = nil
	_, err = workbenchRead(t, h, 2, "", "")
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestWorkbenchMissingOptionalSourcesBoundsStableActivityAndOverflow(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
	out, err := workbenchRead(t, h, 2, "2026-10-19", "2026-10-21")
	require.NoError(t, err)
	require.Nil(t, out.ExecutionCreated)
	require.Nil(t, out.ExecutionCostMicros)
	require.Contains(t, out.MissingSources, "EXECUTIONS")
	h.facts.Work = nil
	for i := 0; i < 12; i++ {
		at := h.m.now().Add(time.Duration(-i) * time.Minute)
		h.facts.Work = append(h.facts.Work, delivery.PlanningWorkFact{ID: guid.ID(i + 1), State: "ACCEPTED", AcceptedAt: &at})
	}
	out, err = workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	require.Equal(t, 12, out.Accepted)
	require.Len(t, out.Acceptances, 10)
	require.Equal(t, guid.ID(1), out.Acceptances[0].WorkItemID)
	require.Empty(t, out.Load)
	h.facts.Work = []delivery.PlanningWorkFact{{ID: 1}, {ID: 1}}
	_, err = workbenchRead(t, h, 2, "", "")
	require.ErrorIs(t, err, ErrUnavailable)
	h.facts.Work = make([]delivery.PlanningWorkFact, 501)
	for i := range h.facts.Work {
		h.facts.Work[i].ID = guid.ID(i + 1)
	}
	_, err = workbenchRead(t, h, 2, "", "")
	require.ErrorIs(t, err, ErrUnavailable)
	h.facts.Work = nil
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3, 4}, nil }
	h.m.facts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
		cost := int64(math.MaxInt64)
		return delivery.PlanningSnapshot{ExecutionCostMicros: &cost}, nil
	}
	_, err = workbenchRead(t, h, 2, "", "")
	require.ErrorIs(t, err, ErrUnavailable)
	for _, projects := range [][]guid.ID{{3, 3}, {0}, make([]guid.ID, 501)} {
		h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return projects, nil }
		_, err = workbenchRead(t, h, 2, "", "")
		require.ErrorIs(t, err, ErrUnavailable)
	}
}

func TestWorkbenchHTTPUsesCanonicalEnvelopeAndErrors(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return nil, nil }
	out, err := h.m.workbenchHTTP(claims(t, 2), &workbenchInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}})
	require.NoError(t, err)
	require.Zero(t, out.Body.Data.Projects)
	for _, input := range []workbenchInput{{PlanningTenantInput: PlanningTenantInput{TenantID: "bad"}}, {PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, From: "bad"}} {
		_, err = h.m.workbenchHTTP(claims(t, 2), &input)
		require.Error(t, err)
	}
	h.m.runtime = nil
	_, err = h.m.workbenchHTTP(claims(t, 2), &workbenchInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}})
	require.Error(t, err)
}

func TestWorkbenchCostPreservesIntegerPrecisionForBrowserClients(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
	cost := int64(9007199254740993)
	h.facts.ExecutionCostMicros = &cost
	out, err := workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	body, err := json.Marshal(out)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	require.Equal(t, "9007199254740993", decoded["execution_cost_micros"])
}

func TestWorkbenchMachineLoadDoesNotBypassSourceAuthorityOrHideFailures(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return nil, nil }
	want := []fleet.MachineLoad{{ID: 77, Slots: 4, Reserved: 2, State: "ONLINE"}}
	h.m.workbenchMachines = func(context.Context, *gorm.DB, guid.ID) ([]fleet.MachineLoad, error) { return want, nil }
	out, err := workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	require.Equal(t, want, out.Machines)
	h.m.workbenchMachines = func(context.Context, *gorm.DB, guid.ID) ([]fleet.MachineLoad, error) {
		return nil, fleet.ErrMachineForbidden
	}
	out, err = workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	require.Empty(t, out.Machines)
	sentinel := errors.New("machine source database unavailable")
	h.m.workbenchMachines = func(context.Context, *gorm.DB, guid.ID) ([]fleet.MachineLoad, error) { return nil, sentinel }
	_, err = workbenchRead(t, h, 2, "", "")
	require.ErrorIs(t, err, sentinel)
	h.m.workbenchMachines = nil
	out, err = workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	require.Contains(t, out.MissingSources, "MACHINE_LOAD")
}

func TestHomeProjectMemberDoesNotNeedTenantAuthority(t *testing.T) {
	h := setup(t)
	h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
	out, err := workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	require.Equal(t, 1, out.Projects)
	require.NotEmpty(t, out.Acceptances)
	// Empty readable scope is a valid Home; denied tenant authority stays denied.
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{}, nil }
	out, err = workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	require.Zero(t, out.Projects)
	require.Empty(t, out.Activity)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.authorize(claims(t, 2), tx, 1, 0, 2, "work_item_read", true)
		return err
	}), ErrForbidden)
}

func TestWorkbenchActivityReturnsOnlyTenNewestAuthorizedFacts(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
	at := h.m.now().Add(-time.Hour)
	for i := 0; i < 12; i++ {
		row := model.Report{TenantID: 1, ID: guid.ID(900 + i), ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: at.AddDate(0, 0, -i).Format(time.DateOnly), State: "CONFIRMED", Content: "{}", ContentHash: "fact", Facts: "{}", Version: 1, CreatedBy: 2, ConfirmedAt: &at}
		require.NoError(t, h.db.Table(h.m.names.Reports.String()).Create(&row).Error)
	}
	out, e := workbenchRead(t, h, 2, "", "")
	require.NoError(t, e)
	require.Len(t, out.Activity, 10)
	for i, a := range out.Activity {
		require.Equal(t, "REPORT_CONFIRMED", a.Kind)
		require.Equal(t, guid.ID(911-i), a.ObjectID)
	}
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("workbench_report_source", func(tx *gorm.DB) {
		if tx.Statement.Table == h.m.names.Reports.String() {
			tx.AddError(ErrUnavailable)
		}
	}))
	_, e = workbenchRead(t, h, 2, "", "")
	require.ErrorIs(t, e, ErrUnavailable)
	require.NoError(t, h.db.Callback().Query().Remove("workbench_report_source"))
}
func TestHomeAuthorityMissingSourcesNeverAuthorizeCurrentActor(t *testing.T) {
	for _, mode := range []string{"lock", "human", "members", "missing"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			switch mode {
			case "lock":
				h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "human":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "members":
				h.m.members = failingMemberChecker{ErrUnavailable}
			case "missing":
				h.m.principals.ActiveHumanByID = nil
			}
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeHomeOn(claims(t, 2), tx, 1) }), ErrUnavailable)
		})
	}
	h := setup(t)
	require.ErrorIs(t, h.m.authorizeHomeOn(claims(t, 2), nil, 1), ErrInvalid)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeHomeOn(claims(t, 2), tx, 2) }), ErrForbidden)
}
