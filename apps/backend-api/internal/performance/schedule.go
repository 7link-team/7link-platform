package performance

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/performance/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// GenerationSchedule is part of the immutable scheme revision. No schedule on
// an older revision means that it never opted into automatic generation.
type GenerationSchedule struct {
	Timezone string   `json:"timezone" minLength:"1" maxLength:"64"`
	Cadences []string `json:"cadences" minItems:"1" maxItems:"4"`
}

func (s GenerationSchedule) validate() error {
	if len(s.Cadences) < 1 || len(s.Cadences) > 4 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, cadence := range s.Cadences {
		if seen[cadence] {
			return ErrInvalid
		}
		seen[cadence] = true
		if _, err := periodWindow(cadence, s.Timezone, time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)); err != nil {
			return err
		}
	}
	return nil
}

type DueReport struct {
	SchemeID  guid.ID      `json:"scheme_id"`
	SubjectID guid.ID      `json:"subject_id"`
	Period    PeriodWindow `json:"period"`
}
type ScheduleStatus struct {
	Pending []DueReport `json:"pending"`
	HasMore bool        `json:"has_more"`
}

// scheduleActivatedOn follows unchanged enabled schedule revisions backwards.
// An ordinary rule edit must not erase a missed generation window.
func (m *Module) scheduleActivatedOn(ctx context.Context, tx *gorm.DB, current RevisionView, subject guid.ID) (time.Time, error) {
	activation := current.CreatedAt
	q := query.BindRevisions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.SchemeID.Eq(current.SchemeID), q.Version.Lt(current.Version)).Order(q.Version.Desc()).Limit(10001).Find()
	if err != nil {
		return activation, err
	}
	if len(rows) > 10000 {
		return activation, ErrUnavailable
	}
	want := scheduleIdentity(current.Definition.Schedule)
	for _, row := range rows {
		previous, e := revisionView(row)
		if e != nil {
			return activation, e
		}
		config := scheduleIdentity(previous.Definition.Schedule)
		if !previous.Definition.Enabled || config != want || previous.Definition.SubjectKind != current.Definition.SubjectKind || !slices.Contains(previous.Definition.SubjectIDs, subject) {
			break
		}
		activation = previous.CreatedAt
	}
	return activation, nil
}

func scheduleIdentity(schedule *GenerationSchedule) string {
	if schedule == nil {
		return "null"
	}
	snapshot := *schedule
	snapshot.Cadences = slices.Clone(schedule.Cadences)
	slices.Sort(snapshot.Cadences)
	raw, _ := json.Marshal(snapshot)
	return string(raw)
}

// dueReportsOn derives backlog from frozen configuration and unique report
// identities. A crash needs no separate cursor recovery or fabricated success.
func (m *Module) dueReportsOn(ctx context.Context, tx *gorm.DB, now time.Time, limit int) (ScheduleStatus, error) {
	out := ScheduleStatus{Pending: []DueReport{}}
	if authn.TenantIDFromContext(ctx) <= 0 || limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	s := query.BindSchemes(tx, m.names)
	var after guid.ID
	for {
		rows, err := s.WithContext(ctx).Where(s.TenantID.Eq(authn.TenantIDFromContext(ctx)), s.Enabled.Is(true), s.ID.Gt(after)).Order(s.ID).Limit(100).Find()
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			after = row.ID
			current, err := m.reportSchemeOn(ctx, tx, row.ID, 0)
			if err != nil {
				return out, err
			}
			config := current.Definition.Schedule
			if config == nil {
				continue
			}
			if err = config.validate(); err != nil {
				return out, err
			}
			activation := current.CreatedAt
			starts := map[guid.ID]time.Time{}
			for _, subject := range current.Definition.SubjectIDs {
				start, err := m.scheduleActivatedOn(ctx, tx, current, subject)
				if err != nil {
					return out, err
				}
				starts[subject] = start
				if start.Before(activation) {
					activation = start
				}
			}
			for _, cadence := range config.Cadences {
				window, err := periodWindow(cadence, config.Timezone, activation)
				if err != nil {
					return out, err
				}
				for !window.CutoffAt.After(now) {
					if !window.CutoffAt.Before(activation) {
						for _, subject := range current.Definition.SubjectIDs {
							if window.CutoffAt.Before(starts[subject]) {
								continue
							}
							q := query.BindReports(tx, m.names)
							count, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.SchemeID.Eq(row.ID), q.SubjectKind.Eq(current.Definition.SubjectKind), q.SubjectID.Eq(subject), q.Cadence.Eq(cadence), q.PeriodStart.Eq(window.PeriodStart), q.PeriodEnd.Eq(window.PeriodEnd)).Count()
							if err != nil {
								return out, err
							}
							if count == 0 {
								if len(out.Pending) == limit {
									out.HasMore = true
									return out, nil
								}
								out.Pending = append(out.Pending, DueReport{SchemeID: row.ID, SubjectID: subject, Period: window})
							}
						}
					}
					window, err = periodWindow(cadence, config.Timezone, window.EndAt)
					if err != nil {
						return out, err
					}
				}
			}
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
}

func (m *Module) generateScheduledOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, due DueReport, now time.Time) (ReportView, error) {
	if tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) != 0 {
		return ReportView{}, ErrForbidden
	}
	if tx == nil || tx.Statement == nil {
		return ReportView{}, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ReportView{}, ErrInvalid
	}
	kind, err := m.generationSubjectKindOn(ctx, tx, due.SchemeID)
	if err != nil {
		return ReportView{}, err
	}
	if err = m.lockReportPrincipalsOn(ctx, tx, kind, due.SubjectID); err != nil {
		return ReportView{}, err
	}
	current, err := m.reportSchemeOn(ctx, tx, due.SchemeID, 0)
	if err != nil {
		return ReportView{}, err
	}
	config := current.Definition.Schedule
	if current.Definition.SubjectKind != kind {
		return ReportView{}, ErrConflict
	}
	if !current.Definition.Enabled || config == nil || config.Timezone != due.Period.Timezone || !slices.Contains(config.Cadences, due.Period.Cadence) || !slices.Contains(current.Definition.SubjectIDs, due.SubjectID) || due.Period.CutoffAt.After(now) {
		return ReportView{}, ErrInvalid
	}
	activation, err := m.scheduleActivatedOn(ctx, tx, current, due.SubjectID)
	if err != nil {
		return ReportView{}, err
	}
	if due.Period.CutoffAt.Before(activation) {
		return ReportView{}, ErrInvalid
	}
	return m.generateReportCoreOn(ctx, tx, due.SchemeID, due.SubjectID, due.Period, "Scheduled generation; evidence cutoff excludes the final calendar day")
}

func (m *Module) runSchedule(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return jobs.Permanent(ErrInvalid)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return ErrUnavailable
	}
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: job.TenantID})
	db := m.runtime.Database().Write()
	now := time.Now().UTC()
	var due ScheduleStatus
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var err error; due, err = m.dueReportsOn(ctx, tx, now, 25); return err }); err != nil {
		return err
	}
	for _, item := range due.Pending {
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { _, err := m.generateScheduledOn(ctx, tx, job.TenantID, item, now); return err }); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{{Kind: "performance.generate_due", Timeout: 5 * time.Minute, Run: m.runSchedule}}
}
func (m *Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: "performance.generate_due", Kind: "performance.generate_due", Cron: "* * * * *", Timezone: "UTC", TenantScope: jobs.ScopePerTenant, Misfire: jobs.MisfireCoalesce, Payload: map[string]any{}}}
}

func (m *Module) registerScheduleHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "performance.schedule.status", Method: "GET", Path: "/performance-schedule-status", Summary: "Read due assessment periods that still require generation", Tags: []string{"tenants"}, Errors: []int{401, 403, 422, 500, 503}}, authz.Guard{Resource: "performance", Verb: "manage"}, func(ctx context.Context, _ *struct{}) (*respx.Body[ScheduleStatus], error) {
		var out ScheduleStatus
		err := m.transaction(ctx, func(tx *gorm.DB) error {
			var err error
			out, err = m.dueReportsOn(ctx, tx, time.Now().UTC(), 100)
			return err
		})
		if err != nil {
			return nil, failure(ctx, err)
		}
		return respx.OK(ctx, out), nil
	})
}

var (
	_ jobs.HandlerProvider  = (*Module)(nil)
	_ jobs.ScheduleProvider = (*Module)(nil)
)
