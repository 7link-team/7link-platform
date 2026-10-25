package planning

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// ReportTimelinessDay uses deadlines as its UTC cohort. A missing deadline or
// an absent submission receipt is never interpreted as an on-time submission.
type ReportTimelinessDay struct {
	Date        string   `json:"date"`
	Due         int      `json:"due"`
	Submitted   int      `json:"submitted"`
	OnTime      int      `json:"on_time"`
	Late        int      `json:"late"`
	Unavailable int      `json:"unavailable" doc:"Reports without an immutable submission receipt; no historical submission is inferred from mutable report state."`
	OnTimeRate  *float64 `json:"on_time_rate"`
}

func (m *Module) recordReportSubmissionOn(ctx context.Context, tx *gorm.DB, row *model.Report) error {
	if m.ids == nil {
		return ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	receipt := &model.ReportSubmission{TenantID: row.TenantID, ID: guid.ID(id), ReportID: row.ID, ReportVersion: row.Version + 1, SubmittedAt: m.now(), DueAt: row.DueAt}
	return query.BindReportSubmissions(tx, m.names).WithContext(ctx).Create(receipt)
}

type ReportTimelinessHistory struct {
	From             time.Time             `json:"from"`
	To               time.Time             `json:"to"`
	Days             []ReportTimelinessDay `json:"days"`
	MissingDeadlines int                   `json:"missing_deadlines" doc:"Visible reports created in the window without a persisted deadline; excluded from rates."`
}

func historyWindow(now time.Time, from, to string) (time.Time, time.Time, error) {
	start, end := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -7), now.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
	var err error
	if from != "" {
		start, err = time.Parse(time.DateOnly, from)
		if err != nil {
			return start, end, ErrInvalid
		}
	}
	if to != "" {
		end, err = time.Parse(time.DateOnly, to)
		if err != nil {
			return start, end, ErrInvalid
		}
	}
	if !start.Before(end) || end.Sub(start) > 93*24*time.Hour {
		return start, end, ErrInvalid
	}
	return start, end, nil
}

func (m *Module) reportTimelinessOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, from, to string) (ReportTimelinessHistory, error) {
	out := ReportTimelinessHistory{Days: []ReportTimelinessDay{}}
	if m == nil || m.now == nil {
		return out, ErrUnavailable
	}
	start, end, err := historyWindow(m.now(), from, to)
	if err != nil {
		return out, err
	}
	if _, err = m.authorize(ctx, tx, tenant, 0, authn.PrincipalIDFromContext(ctx), "report_read", true); err != nil {
		return out, err
	}
	out.From, out.To = start, end
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		out.Days = append(out.Days, ReportTimelinessDay{Date: day.Format(time.DateOnly)})
	}
	q := query.BindReports(tx, m.names)
	// Include undated rows only to disclose excluded facts; count deadline
	// cohorts independently of mutable updated_at and confirmation timestamps.
	stmt := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.WithContext(ctx).Where(q.DueAt.Gte(start), q.DueAt.Lt(end)).Or(q.WithContext(ctx).Where(q.DueAt.IsNull(), q.CreatedAt.Gte(start), q.CreatedAt.Lt(end)))).Order(q.ID)
	visible := 0
	for offset := 0; ; offset += 200 {
		rows, err := stmt.Limit(200).Offset(offset).Find()
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			_, err = m.authorize(ctx, tx, tenant, row.ProjectID, row.PrincipalID, "report_read", false)
			if errors.Is(err, ErrForbidden) || errors.Is(err, delivery.ErrWorkForbidden) || errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return out, err
			}
			visible++
			if visible > 10000 {
				return out, ErrUnavailable
			}
			if row.DueAt == nil {
				out.MissingDeadlines++
				continue
			}
			i := int(row.DueAt.UTC().Sub(start) / (24 * time.Hour))
			day := &out.Days[i]
			day.Due++
			s := query.BindReportSubmissions(tx, m.names)
			receipt, e := s.WithContext(ctx).Where(s.TenantID.Eq(tenant), s.ReportID.Eq(row.ID)).Order(s.SubmittedAt, s.ID).First()
			if errors.Is(e, gorm.ErrRecordNotFound) {
				day.Unavailable++
				continue
			}
			if e != nil {
				return out, e
			}
			if receipt.DueAt == nil || !receipt.DueAt.Equal(*row.DueAt) {
				day.Unavailable++
				continue
			}
			day.Submitted++
			if receipt.SubmittedAt.After(*receipt.DueAt) {
				day.Late++
			} else {
				day.OnTime++
			}
		}
		if len(rows) < 200 {
			break
		}
	}
	for i := range out.Days {
		day := &out.Days[i]
		if day.Due > 0 && day.Unavailable == 0 {
			rate := float64(day.OnTime) / float64(day.Due)
			day.OnTimeRate = &rate
		}
	}
	return out, nil
}

func (m *Module) registerMetricsHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "planning.metrics.report_timeliness", Method: "GET", Path: "/metrics/report-timeliness", Summary: "Read historical report deadline cohorts from persisted submission facts", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}, authz.Guard{Resource: "report", Verb: "read"}, func(ctx context.Context, in *workbenchInput) (*respx.Body[ReportTimelinessHistory], error) {
		tenant, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, planningError(ctx, ErrInvalid)
		}
		db, err := m.database()
		if err != nil {
			return nil, planningError(ctx, err)
		}
		var out ReportTimelinessHistory
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.reportTimelinessOn(ctx, tx, tenant, in.From, in.To)
			return err
		})
		if err != nil {
			return nil, planningError(ctx, err)
		}
		return respx.OK(ctx, out), nil
	})
}
