package performance

import (
	"context"
	"regexp"
	"slices"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// MetricValue uses integer basis points (0..10000) for all *_SCORE and *_RATIO
// metrics; REWORK_COUNT is an integer count. EvidenceRefs name frozen facts.
type MetricValue struct {
	Metric       string   `json:"metric"`
	Value        int64    `json:"value"`
	EvidenceRefs []string `json:"evidence_refs"`
}

// PerformanceEvidenceFact is a source-owner receipt, not a present-day object projection.
// SHA256 identifies the frozen source content; Role distinguishes execution,
// accountability and decisions so a decision cannot silently become output.
type PerformanceEvidenceFact struct {
	WorkID      guid.ID   `json:"work_id,omitempty"`
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Version     int       `json:"version"`
	SHA256      string    `json:"sha256"`
	ProjectID   guid.ID   `json:"project_id"`
	OccurredAt  time.Time `json:"occurred_at"`
	SubjectID   guid.ID   `json:"subject_id"`
	SubjectKind string    `json:"subject_kind"`
	Role        string    `json:"role"`
	Reference   string    `json:"reference"`
}

type WorkSummaryItem struct {
	WorkID       guid.ID  `json:"work_id"`
	ProjectID    guid.ID  `json:"project_id"`
	Title        string   `json:"title"`
	Completed    *bool    `json:"completed"`
	InProgress   *bool    `json:"in_progress"`
	Delayed      *bool    `json:"delayed"`
	Changed      *bool    `json:"changed"`
	EvidenceRefs []string `json:"evidence_refs"`
	Gaps         []string `json:"gaps"`
}

type EvidenceSet struct {
	Facts   []PerformanceEvidenceFact `json:"facts"`
	Metrics []MetricValue             `json:"metrics"`
	Gaps    []string                  `json:"gaps"`
	Work    []WorkSummaryItem         `json:"work"`
}

type EvidenceRequest struct {
	TenantID    guid.ID
	SubjectID   guid.ID
	SubjectKind string
	Period      PeriodWindow
}

// EvidenceReader retains owner authorization and runs in the caller transaction.
// SubjectOn validates actual tenant membership/type, never free-text cohorts.
// ReadOn must attribute/deduplicate fixed facts and state source coverage gaps.
// Metrics contains only fully measured values; omit an incomplete metric.
// Gaps also describes unrelated work-summary limitations and does not invalidate
// independently measured metrics.
type EvidenceReader struct {
	SubjectOn func(context.Context, *gorm.DB, guid.ID, string, guid.ID) error
	ReadOn    func(context.Context, *gorm.DB, EvidenceRequest) (EvidenceSet, error)
}

type EvidenceReaderConnector struct {
	Bind func(EvidenceReader) error
}

func (m *Module) ProvideServices(s *module.Services) error {
	return module.Provide[EvidenceReaderConnector](s, EvidenceReaderConnector{Bind: func(reader EvidenceReader) error {
		if m.initialized || m.evidence.ReadOn != nil || reader.SubjectOn == nil || reader.ReadOn == nil {
			return ErrInvalid
		}
		m.evidence = reader
		return nil
	}})
}

var reportDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validateEvidence(e EvidenceSet, request EvidenceRequest) error {
	if len(e.Facts) > 10000 || len(e.Work) > 10000 || len(e.Metrics) > 8 || len(e.Gaps) > 100 {
		return ErrUnavailable
	}
	refs := map[string]bool{}
	for _, fact := range e.Facts {
		if !textOK(fact.ID, 200) || refs[fact.ID] || fact.Version < 1 || !reportDigestPattern.MatchString(fact.SHA256) || fact.ProjectID <= 0 || fact.SubjectID != request.SubjectID || fact.SubjectKind != request.SubjectKind || !slices.Contains([]string{"EXECUTOR", "ACCOUNTABLE", "DECISION_ACTOR", "SUBJECT"}, fact.Role) || !textOK(fact.Reference, 2000) || !slices.Contains([]string{"WORK_ITEM", "RESULT", "PLAN", "GOAL", "DECISION", "REPORT", "HUMAN_REVIEW", "DOWNSTREAM_RECEIPT"}, fact.Kind) || fact.OccurredAt.IsZero() || (fact.Kind != "PLAN" && fact.OccurredAt.Before(request.Period.StartAt)) || !fact.OccurredAt.Before(request.Period.CutoffAt) {
			return ErrUnavailable
		}
		refs[fact.ID] = true
	}
	validRefs := func(ids []string) bool {
		seen := map[string]bool{}
		for _, id := range ids {
			if !refs[id] || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	}
	metrics := map[string]bool{}
	for _, metric := range e.Metrics {
		if !slices.Contains([]string{"QUALITY_SCORE", "ON_TIME_RATIO", "PROGRESS_RATIO", "REWORK_COUNT", "ADOPTION_SCORE", "DOWNSTREAM_VALUE_SCORE", "RISK_RESPONSE_SCORE"}, metric.Metric) || metrics[metric.Metric] || metric.Value < 0 || metric.Value > 1000000000 || (metric.Metric != "REWORK_COUNT" && metric.Value > 10000) || len(metric.EvidenceRefs) == 0 || !validRefs(metric.EvidenceRefs) {
			return ErrUnavailable
		}
		metrics[metric.Metric] = true
	}
	for _, gap := range e.Gaps {
		if !textOK(gap, 2000) {
			return ErrUnavailable
		}
	}
	for _, work := range e.Work {
		if work.WorkID <= 0 || work.ProjectID <= 0 || !textOK(work.Title, 1000) || !validRefs(work.EvidenceRefs) || ((work.Completed != nil || work.InProgress != nil || work.Delayed != nil || work.Changed != nil) && len(work.EvidenceRefs) == 0) {
			return ErrUnavailable
		}
		for _, gap := range work.Gaps {
			if !textOK(gap, 2000) {
				return ErrUnavailable
			}
		}
	}
	return nil
}
