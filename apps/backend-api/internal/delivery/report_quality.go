package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"database/sql/driver"
	"errors"
	"gorm.io/gorm"
	"time"
)

type ReportQualityMetrics struct {
	L3Entered             int64    `json:"l3_entered"`
	EscapesHuman          int64    `json:"escapes_human"`
	EscapesClient         int64    `json:"escapes_client"`
	InternalQACompleted   int64    `json:"internal_qa_completed"`
	InternalFirstPass     int64    `json:"internal_first_pass"`
	InternalRoundsSum     int64    `json:"internal_rounds_sum"`
	EscapeRate            *float64 `json:"escape_rate,omitempty"`
	InternalFirstPassRate *float64 `json:"internal_first_pass_rate,omitempty"`
	AvgInternalRounds     *float64 `json:"avg_internal_rounds,omitempty"`
}

func (q *ReportQualityMetrics) ComputeRates() {
	q.EscapeRate = nil
	q.InternalFirstPassRate = nil
	q.AvgInternalRounds = nil
	if q.L3Entered > 0 {
		v := float64(q.EscapesHuman+q.EscapesClient) / float64(q.L3Entered)
		q.EscapeRate = &v
	}
	if q.InternalQACompleted > 0 {
		v := float64(q.InternalFirstPass) / float64(q.InternalQACompleted)
		r := float64(q.InternalRoundsSum) / float64(q.InternalQACompleted)
		q.InternalFirstPassRate = &v
		q.AvgInternalRounds = &r
	}
}
func (q *ReportQualityMetrics) Add(part *ReportQualityMetrics) {
	q.L3Entered += part.L3Entered
	q.EscapesHuman += part.EscapesHuman
	q.EscapesClient += part.EscapesClient
	q.InternalQACompleted += part.InternalQACompleted
	q.InternalFirstPass += part.InternalFirstPass
	q.InternalRoundsSum += part.InternalRoundsSum
	q.ComputeRates()
}

// ReportQualityReader only consumes source-authorized work IDs from planning's
// transaction. It cannot enumerate or enlarge the caller's project/work scope.
type ReportQualityReader func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (*ReportQualityMetrics, error)

// ReportMemberQualityReader attributes immutable gate receipts to their actual
// result submitter, independently of current accountable ownership.
type ReportMemberQualityReader func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (*ReportQualityMetrics, error)

func (m *Module) provideReportQuality(s *module.Services) error {
	if err := module.Provide[ReportQualityReader](s, m.reportQualityOn); err != nil {
		return err
	}
	return module.Provide[ReportMemberQualityReader](s, m.reportMemberQualityOn)
}
func (m *Module) reportMemberQualityOn(ctx context.Context, tx *gorm.DB, tenant, project, subject guid.ID, ids []guid.ID, start, end time.Time) (*ReportQualityMetrics, error) {
	if subject <= 0 {
		return nil, ErrInvalidArgument
	}
	return m.reportQualityForOn(ctx, tx, tenant, project, subject, ids, start, end)
}
func (m *Module) reportQualityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, ids []guid.ID, start, end time.Time) (*ReportQualityMetrics, error) {
	return m.reportQualityForOn(ctx, tx, tenant, project, 0, ids, start, end)
}
func (m *Module) reportQualityForOn(ctx context.Context, tx *gorm.DB, tenant, project, subject guid.ID, ids []guid.ID, start, end time.Time) (*ReportQualityMetrics, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || !start.Before(end) || len(ids) > 500 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	marker := query.BindReportQualityObservations(tx, m.service.names)
	observed, err := marker.WithContext(ctx).Where(marker.Singleton.Eq(1)).First()
	if err != nil {
		return nil, err
	}
	if start.Before(observed.ObservedSince) {
		return nil, nil
	}
	out := &ReportQualityMetrics{}
	if len(ids) == 0 {
		return out, nil
	}
	values := make([]driver.Valuer, len(ids))
	for i, id := range ids {
		if id <= 0 {
			return nil, ErrInvalidArgument
		}
		values[i] = id
	}
	q := query.BindReportQualityReceipts(tx, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.In(values...), q.OccurredAt.Gte(start.UTC()), q.OccurredAt.Lt(end.UTC())).Limit(10001).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 10000 {
		return nil, ErrUnavailable
	}
	// A legacy work item may already have passed before observation began.
	// Its first observed success cannot prove a lifetime first-pass numerator.
	qaWork := []driver.Valuer{}
	for _, r := range rows {
		if r.Kind == "INTERNAL_QA_PASSED" {
			qaWork = append(qaWork, r.WorkItemID)
		}
	}
	if len(qaWork) > 0 {
		w := query.BindWorkItems(tx, m.service.names)
		legacy, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.In(qaWork...), w.CreatedAt.Lt(observed.ObservedSince.UTC())).Count()
		if err != nil {
			return nil, err
		}
		if legacy > 0 {
			return nil, nil
		}
	}
	if subject > 0 {
		// Unknown historical identity cannot become a measured zero for any member.
		for _, r := range rows {
			if r.ExecutorPrincipalID == nil {
				return nil, nil
			}
		}
	}
	for _, r := range rows {
		if subject > 0 && *r.ExecutorPrincipalID != subject {
			continue
		}
		switch r.Kind {
		case "L3_ENTERED":
			out.L3Entered++
		case "INTERNAL_QA_PASSED":
			out.InternalQACompleted++
			out.InternalRoundsSum += int64(r.Rounds)
			if r.Rounds == 1 {
				out.InternalFirstPass++
			}
		case "ESCAPE_HUMAN":
			out.EscapesHuman++
		case "ESCAPE_CLIENT":
			out.EscapesClient++
		}
	}
	out.ComputeRates()
	return out, nil
}
func (m *Module) recordReportQualityOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, kind string, source guid.ID, rounds int, at time.Time, resultIDs ...guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || work <= 0 || rounds < 0 || at.IsZero() {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m.ids == nil {
		return ErrUnavailable
	}
	q := query.BindReportQualityReceipts(tx, m.service.names)
	_, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.WorkItemID.Eq(work), q.Kind.Eq(kind), q.SourceID.Eq(source)).First()
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var executor *guid.ID
	if len(resultIDs) > 1 {
		return ErrInvalidArgument
	}
	if len(resultIDs) == 1 {
		r := query.BindResultVersions(tx, m.service.names)
		result, e := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.WorkItemID.Eq(work), r.ID.Eq(resultIDs[0])).Take()
		if e != nil {
			return e
		}
		if result.ActorID <= 0 {
			return ErrInvalidArgument
		}
		executor = &result.ActorID
	}
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	return q.WithContext(ctx).Create(&model.ReportQualityReceipt{TenantID: tenant, ID: guid.ID(id), ProjectID: project, WorkItemID: work, ExecutorPrincipalID: executor, Kind: kind, SourceID: source, Rounds: rounds, OccurredAt: at.UTC()})
}
func (m *Module) recordReportL3On(ctx context.Context, tx *gorm.DB, work *model.WorkItem, before string, at time.Time) error {
	if before == "NEEDS_HUMAN_REVIEW" || work.State != "NEEDS_HUMAN_REVIEW" {
		return nil
	}
	q := query.BindWorkTransitions(tx, m.service.names)
	n, err := q.WithContext(ctx).Where(q.TenantID.Eq(work.TenantID), q.WorkItemID.Eq(work.ID), q.ToState.Eq("NEEDS_HUMAN_REVIEW")).Count()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if work.CurrentClaimID == nil {
		return m.recordReportQualityOn(ctx, tx, work.TenantID, work.ProjectID, work.ID, "L3_ENTERED", 0, 0, at)
	}
	return m.recordReportQualityOn(ctx, tx, work.TenantID, work.ProjectID, work.ID, "L3_ENTERED", 0, 0, at, *work.CurrentClaimID)
}
