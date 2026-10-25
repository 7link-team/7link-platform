package governance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type incidentListInput struct {
	TenantID  string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ProjectID string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	State     string `query:"state" enum:"OPEN,ACKNOWLEDGED,RESOLVED,DISMISSED"`
	Cursor    string `query:"cursor" maxLength:"2048"`
	Limit     int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Sort      string `query:"sort" enum:"-id"`
}
type incidentPage struct {
	Items      []IncidentView `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
}
type incidentCursor struct {
	TenantID, PrincipalID, ProjectID, After guid.ID
	State                                   string
}
type incidentDecisionInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID       string `path:"incident_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch  string `header:"If-Match" required:"true" doc:"Quoted current incident version."`
	Body     struct {
		Reason string `json:"reason,omitempty" maxLength:"2000"`
	}
}

func (m *Module) registerEscalationHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "governance.incidents.list", Method: http.MethodGet, Path: "/escalation-incidents", Summary: "List visible escalation incidents", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}, authz.Guard{Resource: "policy", Verb: "read"}, m.listIncidentsHTTP)
	for _, action := range []struct{ id, path, state, verb string }{{"governance.incident.acknowledge", "acknowledgements", "ACKNOWLEDGED", "read"}, {"governance.incident.dismiss", "dismissals", "DISMISSED", "manage"}} {
		authz.Register(m.registrar, api, huma.Operation{OperationID: action.id, Method: http.MethodPost, Path: "/escalation-incidents/{incident_id}/" + action.path, Summary: "Record a Human escalation disposition", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.Guard{Resource: "policy", Verb: action.verb}, func(ctx context.Context, in *incidentDecisionInput) (*respx.Body[IncidentView], error) {
			return m.decideIncidentHTTP(ctx, in, action.state)
		})
	}
}

func (m *Module) listIncidentsHTTP(ctx context.Context, in *incidentListInput) (*respx.Body[incidentPage], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	if authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return nil, inboxHTTPError(ctx, ErrForbidden)
	}
	if in.Limit < 1 || in.Limit > 100 || (in.Sort != "" && in.Sort != "-id") || (in.State != "" && !slices.Contains([]string{"OPEN", "ACKNOWLEDGED", "RESOLVED", "DISMISSED"}, in.State)) {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	var project guid.ID
	if in.ProjectID != "" {
		project, err = guid.RequireID(in.ProjectID)
		if err != nil {
			return nil, inboxHTTPError(ctx, ErrInvalidArgument)
		}
	}
	scope := incidentCursor{TenantID: tenant, PrincipalID: authn.PrincipalIDFromContext(ctx), ProjectID: project, State: in.State}
	if in.Cursor != "" {
		var c incidentCursor
		data, e := base64.RawURLEncoding.DecodeString(in.Cursor)
		if e != nil || json.Unmarshal(data, &c) != nil || c.After <= 0 {
			return nil, inboxHTTPError(ctx, ErrInvalidArgument)
		}
		after := c.After
		c.After = 0
		if c != scope {
			return nil, inboxHTTPError(ctx, ErrInvalidArgument)
		}
		scope.After = after
	}
	db, err := m.writer()
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	page := incidentPage{Items: []IncidentView{}}
	q := query.BindEscalationIncidents(db, m.seed.names)
	after := scope.After
	for len(page.Items) <= in.Limit {
		s := q.WithContext(ctx).Where(q.TenantID.Eq(tenant))
		if project > 0 {
			s = s.Where(q.ProjectID.Eq(project))
		}
		if after > 0 {
			s = s.Where(q.ID.Lt(after))
		}
		if in.State != "" {
			s = s.Where(q.State.Eq(in.State))
		}
		rows, e := s.Order(q.ID.Desc()).Limit(101).Find()
		if e != nil {
			return nil, inboxHTTPError(ctx, e)
		}
		for _, row := range rows {
			after = row.ID
			allowed, e := m.canReadIncidentProject(ctx, db, tenant, row.ProjectID)
			if e != nil {
				return nil, inboxHTTPError(ctx, e)
			}
			if allowed {
				page.Items = append(page.Items, incidentView(row))
				if len(page.Items) > in.Limit {
					break
				}
			}
		}
		if len(rows) < 101 || len(page.Items) > in.Limit {
			break
		}
	}
	if len(page.Items) > in.Limit {
		page.HasMore = true
		page.Items = page.Items[:in.Limit]
		scope.After = page.Items[in.Limit-1].ID
		data, e := json.Marshal(scope)
		if e != nil {
			return nil, inboxHTTPError(ctx, e)
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return respx.OK(ctx, page), nil
}

func (m *Module) decideIncidentHTTP(ctx context.Context, in *incidentDecisionInput, state string) (*respx.Body[IncidentView], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	version, err := parseGovernanceVersion(in.IfMatch)
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	var view IncidentView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		view, e = m.incidentDecisionOn(ctx, tx, tenant, id, version, state, in.Body.Reason)
		return e
	})
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	return respx.OK(ctx, view), nil
}
