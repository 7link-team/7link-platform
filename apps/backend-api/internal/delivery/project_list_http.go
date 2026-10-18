package delivery

import (
	"context"
	"errors"
	"net/url"

	"7link.com/platform/pkg/extension/huma/respx"
	platformquery "7link.com/platform/pkg/query"
	"github.com/danielgtaylor/huma/v2"
)

type projectListHTTPInput struct {
	TenantID       string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset         int    `query:"offset" default:"0" minimum:"0"`
	Limit          int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Sort           string `query:"sort" doc:"Up to three comma-separated keys: name, stage_changed_at, health, target_date, created_at. Prefix - for descending. Default -created_at."`
	Stage          string `query:"stage" doc:"eq:STATE or in:STATE,STATE; INTAKE, DISCOVERY, PLANNING, ACTIVE, BLOCKED, ACCEPTANCE_PENDING, DELIVERED, CLOSED, CANCELLED."` //nolint:misspell // Frozen business state.
	TeamMode       string `query:"team_mode" doc:"eq:HUMAN_ONLY, eq:HYBRID or eq:AUTONOMOUS."`
	Health         string `query:"health" doc:"eq:VALUE or in:VALUE,VALUE; GREEN, AMBER, RED, NONE."`
	ClientEntityID string `query:"client_entity_id" doc:"eq:ID of the client entity."`
	Code           string `query:"code" doc:"eq:CODE exact project code."`
	Name           string `query:"name" doc:"like:TEXT matches project names containing TEXT."`
	Archived       string `query:"archived" doc:"eq:true or eq:false; default eq:false."`
	LeadAgentID    string `query:"lead_agent_id" doc:"eq:ID of the lead agent."`
	RawQuery       url.Values
}

func (in *projectListHTTPInput) Resolve(ctx huma.Context) []error {
	u := ctx.URL()
	in.RawQuery = u.Query()
	return nil
}

func (m *Module) listProjectsHTTP(ctx context.Context, in *projectListHTTPInput) (*respx.Body[[]projectListView], error) {
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, projectHTTPError(ctx, ErrUnavailable)
	}
	page, err := m.listProjectRecords(ctx, m.runtime.Database().Write(), projectListInput{RawQuery: in.RawQuery, Sort: in.Sort, Offset: in.Offset, Limit: in.Limit})
	if err != nil {
		var queryErr platformquery.Error
		if errors.As(err, &queryErr) {
			return nil, projectHTTPError(ctx, ErrInvalidArgument)
		}
		return nil, projectHTTPError(ctx, err)
	}
	return respx.List(ctx, page.Records, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(page.Records), Total: page.Total}), nil
}
