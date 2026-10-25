package clientwork

import (
	"context"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// Planning owns the tenant calendar. The later module binds its existing
// settings reader without a reverse dependency from Clientwork to Planning.
type AcceptanceCalendar func(context.Context, *gorm.DB, guid.ID) (string, int, error)
type AcceptanceCalendarConnector struct {
	Bind func(AcceptanceCalendar) error
}

func (m *Module) provideAcceptanceCalendar(s *module.Services) error {
	return module.Provide[AcceptanceCalendarConnector](s, AcceptanceCalendarConnector{Bind: func(read AcceptanceCalendar) error {
		if read == nil {
			return ErrUnavailable
		}
		m.acceptanceCalendar = read
		return nil
	}})
}

type AcceptanceDay struct {
	Date           string   `json:"date"`
	Receipts       int64    `json:"receipts"`
	Accepted       int64    `json:"accepted"`
	AcceptanceRate *float64 `json:"acceptance_rate"`
}
type AcceptanceMetrics struct {
	Timezone        string          `json:"timezone"`
	CalendarVersion int             `json:"calendar_version"`
	Days            []AcceptanceDay `json:"days"`
}
type acceptanceMetricsInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Date     string `query:"date" format:"date"`
	Days     int    `query:"days" default:"14" minimum:"1" maximum:"93"`
}

func (m *Module) registerAcceptanceMetricsHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.acceptance.metrics", Method: "GET", Path: entityBase + "/acceptance-metrics", Summary: "Read actual client receipt counts by tenant calendar day", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "project", Verb: "read"}, m.acceptanceMetricsHTTP)
}
func (m *Module) acceptanceMetricsHTTP(ctx context.Context, in *acceptanceMetricsInput) (*respx.Body[AcceptanceMetrics], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil || in.Days < 1 || in.Days > 93 {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out AcceptanceMetrics
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.authorize(ctx, tx, project, "project_read", false); e != nil {
			return e
		}
		var err error
		out, err = m.acceptanceMetricsOn(ctx, tx, authn.TenantIDFromContext(ctx), project, in.Date, in.Days)
		return err
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) acceptanceMetricsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, date string, days int) (AcceptanceMetrics, error) {
	out := AcceptanceMetrics{Days: []AcceptanceDay{}}
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || days < 1 || days > 93 {
		return out, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalid
	}
	if m.acceptanceCalendar == nil {
		return out, ErrUnavailable
	}
	zone, version, e := m.acceptanceCalendar(ctx, tx, tenant)
	if e != nil {
		return out, e
	}
	loc, e := time.LoadLocation(zone)
	if e != nil || zone == "Local" || version < 1 {
		return out, ErrUnavailable
	}
	if date == "" {
		date = now().In(loc).Format("2006-01-02")
	}
	end, e := time.ParseInLocation("2006-01-02", date, loc)
	if e != nil {
		return out, ErrInvalid
	}
	out.Timezone, out.CalendarVersion = zone, version
	q := query.BindPackageReceipt(tx, m.names)
	for start := end.AddDate(0, 0, 1-days); !start.After(end); start = start.AddDate(0, 0, 1) {
		base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Source.In("PORTAL", "RECORDED"), q.CreatedAt.Gte(start.UTC()), q.CreatedAt.Lt(start.AddDate(0, 0, 1).UTC()))
		n, e := base.Count()
		if e != nil {
			return out, e
		}
		yes, e := base.Where(q.Decision.Eq("ACCEPT")).Count()
		if e != nil {
			return out, e
		}
		day := AcceptanceDay{Date: start.Format("2006-01-02"), Receipts: n, Accepted: yes}
		if n > 0 {
			rate := float64(yes) / float64(n)
			day.AcceptanceRate = &rate
		}
		out.Days = append(out.Days, day)
	}
	return out, nil
}
