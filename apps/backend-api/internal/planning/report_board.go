package planning

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"slices"
	"time"
)

type ReportBoardInput struct {
	PlanningTenantInput
	Start      string `query:"start" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
	End        string `query:"end" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
	PeriodKind string `query:"period_kind" enum:"DAY,WEEK,MONTH,QUARTER,YEAR"`
	Origin     string `query:"origin" enum:"HUMAN,HUMAN_CREATED,AGENT_SPAWNED"`
}
type ReportBoardRow struct {
	QualityMetrics *delivery.ReportQualityMetrics `json:"quality_metrics,omitempty"`
	KRChanges      []GoalKRChange                 `json:"kr_changes"`
	Report         PlanningRecord                 `json:"report"`
	Origin         string                         `json:"origin"`
	ChildTotal     int                            `json:"child_total"`
	ChildSubmitted int                            `json:"child_submitted"`
	ChildConfirmed int                            `json:"child_confirmed"`
	Issues         []PlanningIssue                `json:"issues"`
	CheckResult    string                         `json:"check_result"`
	UnfinishedWork int                            `json:"unfinished_work"`
	Progress       *PlanningReportProgress        `json:"progress,omitempty"`
}
type ReportBoard struct {
	Items            []ReportBoardRow    `json:"items"`
	Members          []ReportBoardMember `json:"members"`
	CalendarTimezone string              `json:"calendar_timezone"`
	Truncated        bool                `json:"truncated"`
}
type ReportBoardMember struct {
	PrincipalID guid.ID `json:"principal_id"`
	Origin      string  `json:"origin"`
}
type ReportReminderInput struct {
	PlanningTenantInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           ReportReminderRequest
}
type ReportReminderRequest struct {
	ReportIDs []guid.ID `json:"report_ids" minItems:"1" maxItems:"100"`
}

func (m *Module) registerReportBoardHTTP(api huma.API) {
	op := func(id, method, path, summary string) huma.Operation {
		status := 200
		if method == "POST" {
			status = 201
		}
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("planning.report.board", "GET", "/reports/board", "Read authorized report hierarchy and calendar facts"), authz.Guard{Resource: "report", Verb: "read"}, func(ctx context.Context, in *ReportBoardInput) (*respx.Body[ReportBoard], error) {
		tenant, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, planningError(ctx, ErrInvalid)
		}
		db, err := m.database()
		if err != nil {
			return nil, planningError(ctx, err)
		}
		var out ReportBoard
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = m.reportBoardOn(ctx, tx, tenant, in); return e })
		if err != nil {
			return nil, planningError(ctx, err)
		}
		return respx.OK(ctx, out), nil
	})
	authz.Register(m.registrar, api, op("planning.report.remind", "POST", "/reports/reminders", "Send reminders without changing report state"), authz.Guard{Resource: "report", Verb: "confirm"}, func(ctx context.Context, in *ReportReminderInput) (*PlanningCommandOutput, error) {
		tenant, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, planningError(ctx, ErrInvalid)
		}
		return m.command(ctx, tenant, in.IdempotencyKey, "report.remind", "/reports/reminders", in.Body, func(tx *gorm.DB) (PlanningRecord, error) {
			return m.remindReportsOn(ctx, tx, tenant, in.Body.ReportIDs)
		})
	})
}

func (m *Module) reportBoardOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in *ReportBoardInput) (ReportBoard, error) {
	out := ReportBoard{Items: []ReportBoardRow{}, Members: []ReportBoardMember{}}
	if _, err := m.authorize(ctx, tx, tenant, 0, 0, "report_read", false); err != nil {
		return out, err
	}
	settings, err := m.readSettingsOn(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	out.CalendarTimezone = settings.Timezone
	if in.Start != "" {
		if _, err := time.Parse("2006-01-02", in.Start); err != nil {
			return out, ErrInvalid
		}
	}
	if in.End != "" {
		if _, err := time.Parse("2006-01-02", in.End); err != nil {
			return out, ErrInvalid
		}
	}
	if in.Start != "" && in.End != "" && in.Start >= in.End {
		return out, ErrInvalid
	}
	q := query.BindReports(tx, m.names)
	d := q.WithContext(ctx).Where(q.TenantID.Eq(tenant))
	if in.Start != "" {
		d = d.Where(q.PeriodStart.Gte(in.Start))
	}
	if in.End != "" {
		d = d.Where(q.PeriodStart.Lt(in.End))
	}
	if in.PeriodKind != "" {
		d = d.Where(q.PeriodKind.Eq(in.PeriodKind))
	}
	rows, err := d.Order(q.PeriodStart.Desc(), q.ID).Limit(501).Find()
	if err != nil {
		return out, err
	}
	out.Truncated = len(rows) > 500
	if out.Truncated {
		rows = rows[:500]
	}
	for _, r := range rows {
		if _, err := m.authorize(ctx, tx, tenant, r.ProjectID, r.PrincipalID, "report_read", false); errors.Is(err, ErrForbidden) {
			continue
		} else if err != nil {
			return out, err
		}
		origin := ""
		if r.PrincipalID > 0 {
			_, human, err := m.principals.ActiveHumanByID(ctx, tx, r.PrincipalID)
			if err != nil {
				return out, err
			}
			if human {
				origin = "HUMAN"
			} else if m.agents.ByPrincipalOn != nil {
				a, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, r.PrincipalID)
				if err != nil {
					return out, err
				}
				if found {
					origin = a.Origin
				}
			}
		}
		if in.Origin != "" && origin != in.Origin {
			continue
		}
		v, err := reportView(r)
		if err != nil {
			return out, err
		}
		row := ReportBoardRow{Report: v, Origin: origin, Issues: []PlanningIssue{}}
		check, err := m.checkReport(ctx, tx, tenant, r)
		if err != nil {
			return out, err
		}
		row.Issues = check.Issues
		for _, work := range check.Facts.Work {
			if work.State != "ACCEPTED" && work.State != "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
				row.UnfinishedWork++
			}
		}
		row.KRChanges, err = m.reportKRChangesOn(ctx, tx, tenant, r)
		if err != nil {
			return out, err
		}
		row.CheckResult = check.Result
		row.Progress = &check.Progress
		row.QualityMetrics = check.Facts.QualityMetrics
		children, err := m.childReportsOn(ctx, tx, tenant, r)
		if err != nil {
			return out, err
		}
		row.ChildTotal = len(children)
		for _, child := range children {
			if child.State == "SUBMITTED" || child.State == "CONFIRMED" {
				row.ChildSubmitted++
			}
			if child.State == "CONFIRMED" {
				row.ChildConfirmed++
			}
		}
		out.Items = append(out.Items, row)
	}
	// Include current members even when no draft has yet been generated.
	// Existing reports also retain historical rows for members who have left.
	seen := map[guid.ID]bool{}
	for _, row := range out.Items {
		if row.Report.PrincipalID > 0 && !seen[row.Report.PrincipalID] {
			out.Members = append(out.Members, ReportBoardMember{PrincipalID: row.Report.PrincipalID, Origin: row.Origin})
			seen[row.Report.PrincipalID] = true
		}
	}
	if m.notificationCandidates != nil {
		var after guid.ID
		for {
			candidates, err := m.notificationCandidates(ctx, tx, after, 100)
			if err != nil {
				return out, err
			}
			for _, id := range candidates {
				if id <= after {
					return out, ErrInvalid
				}
				after = id
				if seen[id] {
					continue
				}
				active, err := m.members.IsMemberActiveOn(ctx, tx, tenant, id)
				if err != nil {
					return out, err
				}
				if !active {
					continue
				}
				if _, err := m.authorize(ctx, tx, tenant, 0, id, "report_read", false); errors.Is(err, ErrForbidden) {
					continue
				} else if err != nil {
					return out, err
				}
				origin := "HUMAN"
				_, human, err := m.principals.ActiveHumanByID(ctx, tx, id)
				if err != nil {
					return out, err
				}
				if !human {
					if m.agents.ByPrincipalOn == nil {
						continue
					}
					agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, id)
					if err != nil {
						return out, err
					}
					if !found {
						continue
					}
					origin = agent.Origin
				}
				if in.Origin != "" && in.Origin != origin {
					continue
				}
				out.Members = append(out.Members, ReportBoardMember{PrincipalID: id, Origin: origin})
				seen[id] = true
			}
			if len(candidates) < 100 {
				break
			}
		}
	}
	return out, nil
}

func (m *Module) remindReportsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, ids []guid.ID) (PlanningRecord, error) {
	var out PlanningRecord
	if len(ids) < 1 || len(ids) > 100 {
		return out, ErrInvalid
	}
	if m.reportNotifier == nil {
		return out, ErrUnavailable
	}
	seen := []guid.ID{}
	for _, id := range ids {
		if id <= 0 || slices.Contains(seen, id) {
			return out, ErrInvalid
		}
		seen = append(seen, id)
		r, kind, err := m.reportOn(ctx, tx, tenant, id, "report_confirm", true)
		if err != nil {
			return out, err
		}
		if r.State == "SUBMITTED" || r.State == "CONFIRMED" {
			return out, ErrConflict
		}
		recipient, err := m.reportRecipientOn(ctx, tx, r)
		if err != nil {
			return out, err
		}
		if recipient <= 0 {
			return out, ErrConflict
		}
		eligible, err := m.planningNotificationEligibleOn(ctx, tx, tenant, id, recipient, "report", r.State)
		if err != nil {
			return out, err
		}
		if !eligible {
			return out, ErrForbidden
		}
		fact, _, err := m.planningNotificationFactOn(ctx, tx, tenant, id, "report")
		if err != nil {
			return out, err
		}
		_, err = m.reportNotifier.Notify(ctx, tx, notification.Notification{TenantID: tenant, ProjectID: r.ProjectID, Recipients: []guid.ID{recipient}, Type: "planning.report_reminder", Severity: notification.Action, TemplateKey: "planning.report_reminder", Permission: "report_read", LinkPath: fact.LinkPath, Params: map[string]string{"report_id": id.String(), "period_kind": r.PeriodKind, "period_start": r.PeriodStart}})
		if err != nil {
			return out, err
		}
		out, err = reportView(r)
		if err != nil {
			return out, err
		}
		if err = m.record(ctx, tx, tenant, out, kind, "REMIND", ""); err != nil {
			return out, err
		}
	}
	return out, nil
}
