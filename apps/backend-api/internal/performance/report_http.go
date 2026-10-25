package performance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/performance/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type GenerateReportBody struct {
	SchemeID   guid.ID `json:"scheme_id"`
	SubjectID  guid.ID `json:"subject_id"`
	Cadence    string  `json:"cadence" enum:"WEEK,MONTH,QUARTER,YEAR"`
	Timezone   string  `json:"timezone" minLength:"1" maxLength:"64"`
	AnchorDate string  `json:"anchor_date" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
	Reason     string  `json:"reason" minLength:"1" maxLength:"2000"`
}
type RecalculateBody struct {
	IncludeUncovered bool   `json:"include_uncovered,omitempty" doc:"Explicitly supplement the final day after the natural period has ended; creates a new frozen revision."`
	BaseVersion      int    `json:"base_version" minimum:"1"`
	SchemeVersion    int    `json:"scheme_version" minimum:"1"`
	Reason           string `json:"reason" minLength:"1" maxLength:"2000"`
}
type CalibrationBody struct {
	BaseVersion  int      `json:"base_version" minimum:"1"`
	DimensionKey string   `json:"dimension_key" minLength:"1" maxLength:"64"`
	RuleKey      string   `json:"rule_key" minLength:"1" maxLength:"64"`
	Value        int64    `json:"value" minimum:"0" maximum:"10000" doc:"Professional review score in basis points (10000 is 100 percent)."`
	EvidenceRefs []string `json:"evidence_refs" minItems:"1" maxItems:"50"`
	Reason       string   `json:"reason" minLength:"1" maxLength:"2000"`
}
type ConfirmationBody struct {
	BaseVersion int    `json:"base_version" minimum:"1"`
	SHA256      string `json:"sha256" pattern:"^[a-f0-9]{64}$"`
	Reason      string `json:"reason" minLength:"1" maxLength:"2000"`
}
type reportGenerateInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           GenerateReportBody
}
type reportRecalculateInput struct {
	ReportID       string `path:"report_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           RecalculateBody
}
type reportCalibrateInput struct {
	ReportID       string `path:"report_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           CalibrationBody
}
type reportConfirmInput struct {
	ReportID       string `path:"report_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           ConfirmationBody
}
type reportReadInput struct {
	ReportID string `path:"report_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Version  int    `query:"version" minimum:"1"`
}
type reportListInput struct {
	SubjectKind string `query:"subject_kind" enum:"HUMAN,AGENT,PROJECT,TEAM"`
	Offset      int    `query:"offset" minimum:"0" default:"0"`
	Limit       int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}
type reportHistoryInput struct {
	ReportID string `path:"report_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}
type ReportPage struct {
	Items      []ReportView `json:"items"`
	HasMore    bool         `json:"has_more"`
	NextOffset int          `json:"next_offset,omitempty"`
}
type periodPreviewInput struct {
	Cadence    string `query:"cadence" enum:"WEEK,MONTH,QUARTER,YEAR" required:"true"`
	Timezone   string `query:"timezone" required:"true" maxLength:"64"`
	AnchorDate string `query:"anchor_date" required:"true" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
}
type PeriodPreview struct {
	Current PeriodWindow `json:"current"`
	Next    PeriodWindow `json:"next"`
}

func (m *Module) registerReportHTTP(api huma.API) {
	guard := authz.Guard{Resource: "performance", Verb: "manage"}
	register := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, register("performance.period.list", "GET", "/performance-periods", "List fixed assessment reports by descending period"), guard, m.listReportsHTTP)
	authz.Register(m.registrar, api, register("performance.period.preview", "GET", "/performance-period-preview", "Preview calendar boundaries, exclusive cutoff and next trigger"), guard, m.previewPeriodHTTP)
	authz.Register(m.registrar, api, register("performance.report.generate", "POST", "/performance-reports", "Generate or find one logical subject assessment report"), guard, m.generateReportHTTP)
	authz.Register(m.registrar, api, register("performance.report.get", "GET", "/performance-reports/{report_id}", "Read a fixed assessment report revision"), guard, m.getReportHTTP)
	authz.Register(m.registrar, api, register("performance.report.history", "GET", "/performance-reports/{report_id}/revisions", "Read immutable assessment report revisions"), guard, m.reportHistoryHTTP)
	authz.Register(m.registrar, api, register("performance.report.recalculate", "POST", "/performance-reports/{report_id}/recalculations", "Recalculate with explicit rules and optional justified final-day evidence"), guard, m.recalculateReportHTTP)
	authz.Register(m.registrar, api, register("performance.report.calibrate", "POST", "/performance-reports/{report_id}/calibrations", "Append an evidenced professional review assessment"), guard, m.calibrateReportHTTP)
	authz.Register(m.registrar, api, register("performance.report.confirm", "POST", "/performance-reports/{report_id}/confirmations", "Confirm an exact complete assessment snapshot"), guard, m.confirmReportHTTP)
}

func reportWindowForDate(cadence, zone, date string) (PeriodWindow, error) {
	d, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return PeriodWindow{}, ErrInvalid
	}
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "Local" || zone == "" {
		return PeriodWindow{}, ErrInvalid
	}
	anchor := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, loc)
	if anchor.Year() != d.Year() || anchor.Month() != d.Month() || anchor.Day() != d.Day() {
		return PeriodWindow{}, ErrInvalid
	}
	return periodWindow(cadence, zone, anchor)
}

func (m *Module) reportCommand(ctx context.Context, id guid.ID, key, operation string, body any, run func(*gorm.DB) (ReportView, error)) (*respx.Body[ReportView], error) {
	raw, err := json.Marshal(struct {
		ID   guid.ID
		Body any
	}{id, body})
	if err != nil {
		return nil, failure(ctx, err)
	}
	var out ReportView
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, failure(ctx, ErrUnavailable)
	}
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var kind string
		var subject guid.ID
		if request, ok := body.(GenerateReportBody); ok {
			var err error
			kind, err = m.generationSubjectKindOn(ctx, tx, request.SchemeID)
			if err != nil {
				return err
			}
			subject = request.SubjectID
		} else {
			q := query.BindReports(tx, m.names)
			head, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Eq(id)).First()
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			kind, subject = head.SubjectKind, head.SubjectID
		}
		if err := m.lockReportPrincipalsOn(ctx, tx, kind, subject); err != nil {
			return err
		}
		if err := m.authorizeOn(ctx, tx); err != nil {
			return err
		}
		tx = tx.WithContext(context.WithValue(ctx, reportPrincipalLocks{}, reportPrincipalLocks{Kind: kind, Subject: subject}))
		if m.idem == nil {
			return ErrUnavailable
		}
		result, _, err := m.idem.Do(tx.Statement.Context, tx, idempotency.Key{TenantID: authn.TenantIDFromContext(ctx), SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation + ":" + id.String(), Value: key, RequestHash: sha256.Sum256(raw)}, func(work *gorm.DB) (idempotency.Result, error) {
			view, err := run(work)
			if err != nil {
				return idempotency.Result{}, err
			}
			encoded, err := json.Marshal(view)
			return idempotency.Result{StatusCode: 200, Body: encoded}, err
		})
		if err != nil {
			return err
		}
		return json.Unmarshal(result.Body, &out)
	})
	if err != nil {
		return nil, failure(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) generateReportHTTP(ctx context.Context, in *reportGenerateInput) (*respx.Body[ReportView], error) {
	window, err := reportWindowForDate(in.Body.Cadence, in.Body.Timezone, in.Body.AnchorDate)
	if err != nil || in.Body.SchemeID <= 0 || in.Body.SubjectID <= 0 {
		return nil, failure(ctx, ErrInvalid)
	}
	return m.reportCommand(ctx, 0, in.IdempotencyKey, "performance.report.generate", in.Body, func(tx *gorm.DB) (ReportView, error) {
		return m.generateReportOn(ctx, tx, in.Body.SchemeID, in.Body.SubjectID, window, in.Body.Reason)
	})
}
func (m *Module) recalculateReportHTTP(ctx context.Context, in *reportRecalculateInput) (*respx.Body[ReportView], error) {
	id, err := guid.RequireID(in.ReportID)
	if err != nil {
		return nil, failure(ctx, ErrInvalid)
	}
	return m.reportCommand(ctx, id, in.IdempotencyKey, "performance.report.recalculate", in.Body, func(tx *gorm.DB) (ReportView, error) {
		return m.recalculateReportOn(ctx, tx, id, in.Body.BaseVersion, in.Body.SchemeVersion, in.Body.Reason, in.Body.IncludeUncovered)
	})
}
func (m *Module) calibrateReportHTTP(ctx context.Context, in *reportCalibrateInput) (*respx.Body[ReportView], error) {
	id, err := guid.RequireID(in.ReportID)
	if err != nil {
		return nil, failure(ctx, ErrInvalid)
	}
	return m.reportCommand(ctx, id, in.IdempotencyKey, "performance.report.calibrate", in.Body, func(tx *gorm.DB) (ReportView, error) { return m.calibrateReportOn(ctx, tx, id, in.Body) })
}
func (m *Module) confirmReportHTTP(ctx context.Context, in *reportConfirmInput) (*respx.Body[ReportView], error) {
	id, err := guid.RequireID(in.ReportID)
	if err != nil {
		return nil, failure(ctx, ErrInvalid)
	}
	return m.reportCommand(ctx, id, in.IdempotencyKey, "performance.report.confirm", in.Body, func(tx *gorm.DB) (ReportView, error) { return m.confirmReportOn(ctx, tx, id, in.Body) })
}
func (m *Module) getReportHTTP(ctx context.Context, in *reportReadInput) (*respx.Body[ReportView], error) {
	id, err := guid.RequireID(in.ReportID)
	if err != nil || in.Version < 0 {
		return nil, failure(ctx, ErrInvalid)
	}
	var out ReportView
	err = m.transaction(ctx, func(tx *gorm.DB) error { _, view, err := m.reportOn(ctx, tx, id, in.Version); out = view; return err })
	if err != nil {
		return nil, failure(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) listReportsHTTP(ctx context.Context, in *reportListInput) (*respx.Body[ReportPage], error) {
	if in.Offset < 0 || in.Limit < 1 || in.Limit > 200 {
		return nil, failure(ctx, ErrInvalid)
	}
	if in.SubjectKind != "" && !slices.Contains([]string{"HUMAN", "AGENT", "PROJECT", "TEAM"}, in.SubjectKind) {
		return nil, failure(ctx, ErrInvalid)
	}
	out := ReportPage{Items: []ReportView{}}
	err := m.transaction(ctx, func(tx *gorm.DB) error {
		q := query.BindReports(tx, m.names)
		read := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)))
		if in.SubjectKind != "" {
			read = read.Where(q.SubjectKind.Eq(in.SubjectKind))
		}
		rows, err := read.Order(q.PeriodStart.Desc(), q.Cadence, q.ID).Offset(in.Offset).Limit(in.Limit + 1).Find()
		if err != nil {
			return err
		}
		out.HasMore = len(rows) > in.Limit
		if out.HasMore {
			rows = rows[:in.Limit]
			out.NextOffset = in.Offset + in.Limit
		}
		for _, row := range rows {
			_, view, err := m.reportOn(ctx, tx, row.ID, 0)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, view)
		}
		return nil
	})
	if err != nil {
		return nil, failure(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) reportHistoryHTTP(ctx context.Context, in *reportHistoryInput) (*respx.Body[ReportPage], error) {
	id, err := guid.RequireID(in.ReportID)
	if err != nil || in.Offset < 0 || in.Limit < 1 || in.Limit > 200 {
		return nil, failure(ctx, ErrInvalid)
	}
	out := ReportPage{Items: []ReportView{}}
	err = m.transaction(ctx, func(tx *gorm.DB) error {
		if _, _, err := m.reportOn(ctx, tx, id, 0); err != nil {
			return err
		}
		q := query.BindReportRevisions(tx, m.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ReportID.Eq(id)).Order(q.Version.Desc()).Offset(in.Offset).Limit(in.Limit + 1).Find()
		if err != nil {
			return err
		}
		out.HasMore = len(rows) > in.Limit
		if out.HasMore {
			rows = rows[:in.Limit]
			out.NextOffset = in.Offset + in.Limit
		}
		for _, row := range rows {
			view, err := reportRevisionView(row)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, view)
		}
		return nil
	})
	if err != nil {
		return nil, failure(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) previewPeriodHTTP(ctx context.Context, in *periodPreviewInput) (*respx.Body[PeriodPreview], error) {
	current, err := reportWindowForDate(in.Cadence, in.Timezone, in.AnchorDate)
	if err != nil {
		return nil, failure(ctx, err)
	}
	var next PeriodWindow
	err = m.transaction(ctx, func(*gorm.DB) error {
		var e error
		next, e = nextPeriodWindow(in.Cadence, in.Timezone, time.Now())
		return e
	})
	if err != nil {
		return nil, failure(ctx, err)
	}
	return respx.OK(ctx, PeriodPreview{Current: current, Next: next}), nil
}
