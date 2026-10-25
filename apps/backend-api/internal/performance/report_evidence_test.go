package performance

import (
	"context"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEvidenceConnectorBindsOnceBeforeInitialize(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.ProvideServices(s))
	require.NoError(t, s.Seal())
	connector, err := module.Resolve[EvidenceReaderConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(EvidenceReader{}), ErrInvalid)
	reader := EvidenceReader{SubjectOn: func(context.Context, *gorm.DB, guid.ID, string, guid.ID) error { return nil }, ReadOn: func(context.Context, *gorm.DB, EvidenceRequest) (EvidenceSet, error) { return EvidenceSet{}, nil }}
	require.NoError(t, connector.Bind(reader))
	require.ErrorIs(t, connector.Bind(reader), ErrInvalid)
	m.initialized = true
	m.evidence = EvidenceReader{}
	require.ErrorIs(t, connector.Bind(reader), ErrInvalid)
}

func TestEvidenceRejectsUnsupportedAttributionCutoffMetricsAndMissingProvenance(t *testing.T) {
	window, err := reportWindowForDate("MONTH", "UTC", "2024-02-12")
	require.NoError(t, err)
	request := EvidenceRequest{TenantID: 1, SubjectID: 21, SubjectKind: "HUMAN", Period: window}
	base := PerformanceEvidenceFact{ID: "result:1", Kind: "RESULT", Version: 1, SHA256: strings.Repeat("a", 64), ProjectID: 3, OccurredAt: window.StartAt.Add(time.Hour), SubjectID: 21, SubjectKind: "HUMAN", Role: "EXECUTOR", Reference: "/fixed/result/1"}
	for _, change := range []func(*PerformanceEvidenceFact){
		func(f *PerformanceEvidenceFact) { f.Version = 0 }, func(f *PerformanceEvidenceFact) { f.SHA256 = "current" }, func(f *PerformanceEvidenceFact) { f.SubjectID = 22 }, func(f *PerformanceEvidenceFact) { f.ProjectID = 0 }, func(f *PerformanceEvidenceFact) { f.Kind = "MESSAGE_COUNT" }, func(f *PerformanceEvidenceFact) { f.OccurredAt = window.CutoffAt }, func(f *PerformanceEvidenceFact) { f.OccurredAt = window.StartAt.Add(-time.Nanosecond) }, func(f *PerformanceEvidenceFact) { f.Role = "CURRENT_OWNER" },
	} {
		fact := base
		change(&fact)
		require.ErrorIs(t, validateEvidence(EvidenceSet{Facts: []PerformanceEvidenceFact{fact}}, request), ErrUnavailable)
	}
	require.ErrorIs(t, validateEvidence(EvidenceSet{Facts: []PerformanceEvidenceFact{base, base}}, request), ErrUnavailable)
	for _, metric := range []MetricValue{
		{Metric: "QUALITY_SCORE", Value: 10001, EvidenceRefs: []string{base.ID}},
		{Metric: "REVIEW_SCORE", Value: 8000, EvidenceRefs: []string{base.ID}},
		{Metric: "QUALITY_SCORE", Value: 8000, EvidenceRefs: []string{"missing"}},
		{Metric: "QUALITY_SCORE", Value: 8000},
	} {
		require.ErrorIs(t, validateEvidence(EvidenceSet{Facts: []PerformanceEvidenceFact{base}, Metrics: []MetricValue{metric}}, request), ErrUnavailable)
	}
	valid := EvidenceSet{Facts: []PerformanceEvidenceFact{base}, Metrics: []MetricValue{{Metric: "QUALITY_SCORE", Value: 0, EvidenceRefs: []string{base.ID}}}}
	require.NoError(t, validateEvidence(valid, request), "observed zero is distinct from missing data")
	baseline := base
	baseline.ID = "plan:1"
	baseline.Kind = "PLAN"
	baseline.OccurredAt = window.StartAt.Add(-24 * time.Hour)
	require.NoError(t, validateEvidence(EvidenceSet{Facts: []PerformanceEvidenceFact{baseline}}, request), "an earlier confirmed commitment can support period due-date analysis")
	done := true
	require.ErrorIs(t, validateEvidence(EvidenceSet{Work: []WorkSummaryItem{{WorkID: 41, ProjectID: 3, Title: "Current status is insufficient", Completed: &done}}}, request), ErrUnavailable)
}
