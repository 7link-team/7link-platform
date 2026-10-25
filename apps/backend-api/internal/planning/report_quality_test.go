package planning

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestReportQualityHydrationPreservesMissingAndSourceDimensions(t *testing.T) {
	h := setup(t)
	start := h.m.now().Add(-time.Hour)
	end := h.m.now()
	out := delivery.PlanningSnapshot{Work: []delivery.PlanningWorkFact{{ID: 100}}, MissingSources: []string{"QUALITY_METRICS", "OTHER"}}
	h.m.reportQuality = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, ids []guid.ID, from, to time.Time) (*delivery.ReportQualityMetrics, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 3, project)
		require.Equal(t, []guid.ID{100}, ids)
		require.Equal(t, start, from)
		require.Equal(t, end, to)
		return nil, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.hydrateReportQualityOn(claims(t, 2), tx, 1, 3, start, end, &out) }))
	require.Nil(t, out.QualityMetrics)
	require.Contains(t, out.MissingSources, "QUALITY_METRICS")
	h.m.reportQuality = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (*delivery.ReportQualityMetrics, error) {
		q := &delivery.ReportQualityMetrics{L3Entered: 20, EscapesHuman: 1, EscapesClient: 1, InternalQACompleted: 20, InternalFirstPass: 14, InternalRoundsSum: 31}
		q.ComputeRates()
		return q, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.hydrateReportQualityOn(claims(t, 2), tx, 1, 3, start, end, &out) }))
	require.NotContains(t, out.MissingSources, "QUALITY_METRICS")
	require.Contains(t, out.MissingSources, "OTHER")
	require.InDelta(t, 1.55, *out.QualityMetrics.AvgInternalRounds, 1e-9)
}

func TestReportQualityTeamAggregatesNumeratorsAndDoesNotEraseUnknown(t *testing.T) {
	h := setup(t)
	start := h.m.now().Add(-time.Hour)
	end := h.m.now()
	unknown := false
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3, 4}, nil }
	h.m.facts.ReadOn = func(_ context.Context, _ *gorm.DB, tenant, project, subject guid.ID, from, to time.Time) (delivery.PlanningSnapshot, error) {
		return delivery.PlanningSnapshot{Work: []delivery.PlanningWorkFact{{ID: project}}, MissingSources: []string{"QUALITY_METRICS"}}, nil
	}
	h.m.reportQuality = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, ids []guid.ID, from, to time.Time) (*delivery.ReportQualityMetrics, error) {
		require.Equal(t, []guid.ID{project}, ids)
		if project == 3 {
			return &delivery.ReportQualityMetrics{L3Entered: 2, EscapesHuman: 1, InternalQACompleted: 2, InternalFirstPass: 2, InternalRoundsSum: 2}, nil
		}
		if unknown {
			return nil, nil
		}
		return &delivery.ReportQualityMetrics{L3Entered: 18, EscapesClient: 1, InternalQACompleted: 18, InternalFirstPass: 12, InternalRoundsSum: 29}, nil
	}
	read := func() delivery.PlanningSnapshot {
		var out delivery.PlanningSnapshot
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = h.m.reportFactsOn(claims(t, 2), tx, 1, &model.Report{}, start, end)
			return err
		}))
		return out
	}
	out := read()
	require.NotNil(t, out.QualityMetrics)
	require.InDelta(t, .1, *out.QualityMetrics.EscapeRate, 1e-9)
	require.InDelta(t, .7, *out.QualityMetrics.InternalFirstPassRate, 1e-9)
	require.InDelta(t, 1.55, *out.QualityMetrics.AvgInternalRounds, 1e-9)
	unknown = true
	out = read()
	require.Nil(t, out.QualityMetrics)
	require.Contains(t, out.MissingSources, "QUALITY_METRICS")
	// An accountable member is not silently treated as the actual executor.
	h.m.reportQuality = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (*delivery.ReportQualityMetrics, error) {
		t.Fatal("accountable principal cannot be substituted for executor quality")
		return nil, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		out, err := h.m.snapshotOn(claims(t, 2), tx, 1, 3, 2, start, end)
		require.Nil(t, out.QualityMetrics)
		return err
	}))
}

func TestReportMemberQualityUsesAuthorizedScopeInsteadOfAccountableScope(t *testing.T) {
	h := setup(t)
	start, end := h.m.now().Add(-time.Hour), h.m.now()
	h.m.facts.ReadOn = func(_ context.Context, _ *gorm.DB, tenant, project, subject guid.ID, from, to time.Time) (delivery.PlanningSnapshot, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 3, project)
		out := delivery.PlanningSnapshot{Work: []delivery.PlanningWorkFact{}, MissingSources: []string{"QUALITY_METRICS"}}
		if subject == 0 {
			out.Work = append(out.Work, delivery.PlanningWorkFact{ID: 100, AccountablePrincipalID: 4})
		}
		return out, nil
	}
	h.m.memberQuality = func(_ context.Context, _ *gorm.DB, tenant, project, subject guid.ID, ids []guid.ID, from, to time.Time) (*delivery.ReportQualityMetrics, error) {
		require.EqualValues(t, 2, subject)
		require.Equal(t, []guid.ID{100}, ids)
		require.Equal(t, start, from)
		require.Equal(t, end, to)
		return &delivery.ReportQualityMetrics{L3Entered: 1, InternalQACompleted: 1, InternalFirstPass: 1, InternalRoundsSum: 1}, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		out, e := h.m.snapshotOn(claims(t, 2), tx, 1, 3, 2, start, end)
		require.Empty(t, out.Work, "accountable work list semantics are preserved")
		require.NotNil(t, out.QualityMetrics)
		require.EqualValues(t, 1, out.QualityMetrics.L3Entered)
		require.NotContains(t, out.MissingSources, "QUALITY_METRICS")
		return e
	}))
}
