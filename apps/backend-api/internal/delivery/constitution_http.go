package delivery

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"

	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformquery "7link.com/platform/pkg/query"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const constitutionPath = "/entities/{entity_id}/project/constitution-versions"
const constitutionVersionPath = "/constitution-versions/{version_id}"

type constitutionCreateInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           constitutionProposalBody
}
type constitutionGetInput struct {
	VersionID string `path:"version_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type constitutionSubmitInput struct {
	VersionID      string `path:"version_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted document concurrency version from ETag."`
	IdempotencyKey string `header:"Idempotency-Key"`
}
type constitutionDecisionInput struct {
	VersionID      string `path:"version_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted document concurrency version from ETag."`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           constitutionDecisionBody
}
type constitutionGetOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[constitutionDetailView]
}
type constitutionListInput struct {
	EntityID      string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset        int    `query:"offset" default:"0" minimum:"0"`
	Limit         int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	State         string `query:"state" doc:"eq:STATE or in:STATE,STATE."`
	ChangeSummary string `query:"change_summary" doc:"like:TEXT proposal summary filter."`
	Sort          string `query:"sort" doc:"version_no, created_at, updated_at; prefix - for descending."`
	RawQuery      url.Values
}

func (in *constitutionListInput) Resolve(ctx huma.Context) []error {
	u := ctx.URL()
	in.RawQuery = u.Query()
	return nil
}

func (m *Module) registerConstitutionHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	owned := authz.ResourceOwnerResolver(m.constitutionOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "constitution.list", Method: http.MethodGet, Path: constitutionPath, Summary: "List project constitution versions", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "read"}, m.listConstitutionsHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "constitution.create", Method: http.MethodPost, Path: constitutionPath, Summary: "Propose a fixed constitution version", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "read"}, m.proposeConstitutionHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "constitution.get", Method: http.MethodGet, Path: constitutionVersionPath, Summary: "Read a constitution document and review", Tags: []string{"constitution-versions"}, Errors: []int{401, 403, 404, 422, 500, 503}}, "version_id", owned, authz.Guard{Resource: "project", Verb: "read"}, m.getConstitutionHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "constitution.submit", Method: http.MethodPost, Path: constitutionVersionPath + "/submissions", Summary: "Submit the proposer's draft for review", Tags: []string{"constitution-versions"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, "version_id", owned, authz.Guard{Resource: "project", Verb: "read"}, m.submitConstitutionHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "constitution.decide", Method: http.MethodPost, Path: constitutionVersionPath + "/decision", Summary: "Record a Human constitution review decision", Tags: []string{"constitution-versions"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, "version_id", owned, authz.Guard{Resource: "project", Verb: "manage"}, m.decideConstitutionHTTP)
	for _, path := range []string{constitutionPath, constitutionVersionPath + "/decision"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}

func constitutionHTTPError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ErrConstitutionTooLarge):
		return respx.ErrStatus(ctx, 422, 127050, deliveryi18n.ConstitutionTooLarge, nil)
	case errors.Is(err, ErrConstitutionState):
		return respx.ErrStatus(ctx, 409, 127052, deliveryi18n.ConstitutionState, nil)
	case errors.Is(err, ErrConstitutionVersion):
		return respx.ErrStatus(ctx, 409, 120409, deliveryi18n.ConstitutionVersion, nil)
	case errors.Is(err, ErrConstitutionHumanOnly):
		return respx.ErrStatus(ctx, 403, 120403, deliveryi18n.ConstitutionHumanOnly, map[string]string{"reason": "HUMAN_ONLY"})
	default:
		return workHTTPError(ctx, err)
	}
}

// Root document routes resolve their trusted tenant/project without exposing body.
// The command rechecks both IDs inside its own authorization/locking boundary.
func (m *Module) constitutionOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.service == nil {
		return authz.ResourceOwner{}, false, ErrUnavailable
	}
	if id <= 0 {
		return authz.ResourceOwner{}, false, ErrInvalidArgument
	}
	q := query.BindConstitutions(m.runtime.Database().Write(), m.service.names)
	row, err := q.WithContext(ctx).Select(q.TenantID, q.ProjectID).Where(q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: row.TenantID, EntityID: row.ProjectID}, true, nil
}

func (m *Module) proposeConstitutionHTTP(ctx context.Context, in *constitutionCreateInput) (*deliveryCommandOutput[ConstitutionView], error) {
	entity, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, constitutionHTTPError(ctx, ErrInvalidArgument)
	}
	return deliveryCommand(ctx, m, "project_constitution", constitutionHTTPError, "constitution.create", http.MethodPost, constitutionPath, in.IdempotencyKey, 201, in.Body, map[string]string{"entity_id": entity.String()}, func(tx *gorm.DB) (ConstitutionView, guid.ID, error) {
		v, e := m.proposeConstitutionOn(ctx, tx, authn.TenantIDFromContext(ctx), entity, authn.PrincipalIDFromContext(ctx), in.Body)
		return v, v.ID, e
	})
}
func (m *Module) submitConstitutionHTTP(ctx context.Context, in *constitutionSubmitInput) (*deliveryCommandOutput[ConstitutionView], error) {
	return m.constitutionChangeHTTP(ctx, in.VersionID, in.IfMatch, in.IdempotencyKey, nil)
}
func (m *Module) decideConstitutionHTTP(ctx context.Context, in *constitutionDecisionInput) (*deliveryCommandOutput[ConstitutionView], error) {
	return m.constitutionChangeHTTP(ctx, in.VersionID, in.IfMatch, in.IdempotencyKey, &in.Body)
}
func (m *Module) constitutionChangeHTTP(ctx context.Context, idRaw, match, key string, body *constitutionDecisionBody) (*deliveryCommandOutput[ConstitutionView], error) {
	entity, id, version, err := deliveryEditIDs(authn.EntityIDFromContext(ctx).String(), idRaw, match)
	if err != nil {
		return nil, constitutionHTTPError(ctx, err)
	}
	action, suffix := "constitution.submit", "/submissions"
	if body != nil {
		action, suffix = "constitution.decide", "/decision"
	}
	request := struct {
		Version  int                       `json:"version"`
		Decision *constitutionDecisionBody `json:"decision,omitempty"`
	}{version, body}
	return deliveryCommand(ctx, m, "project_constitution", constitutionHTTPError, action, http.MethodPost, constitutionVersionPath+suffix, key, 201, request, map[string]string{"version_id": id.String()}, func(tx *gorm.DB) (ConstitutionView, guid.ID, error) {
		v, e := m.changeConstitutionOn(ctx, tx, authn.TenantIDFromContext(ctx), entity, id, authn.PrincipalIDFromContext(ctx), version, body)
		return v, v.ID, e
	})
}

// Reads apply the current project-level data scope before revealing any version.
func (m *Module) constitutionReadProjectOn(ctx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID) error {
	if m == nil || m.service == nil || m.workPolicy == nil {
		return ErrUnavailable
	}
	c, err := m.workPolicy(ctx, tx, tenant, entity, "project_read", actor)
	if err != nil {
		return err
	}
	if !c.AllowsEntityRows(entity) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, entity.String()) && len(c.OwnerIDs) == 0) {
		return ErrWorkForbidden
	}
	p := query.BindProjects(tx, m.service.names)
	scope, err := workScope(c, entity, p.CreatedBy)
	if err != nil {
		return err
	}
	base := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(entity))
	if scope != nil {
		base = base.Where(scope)
	}
	_, err = base.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrProjectNotFound
	}
	return err
}
func (m *Module) getConstitutionHTTP(ctx context.Context, in *constitutionGetInput) (*constitutionGetOutput, error) {
	id, err := guid.RequireID(in.VersionID)
	if err != nil {
		return nil, constitutionHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, constitutionHTTPError(ctx, ErrUnavailable)
	}
	tenant, entity, actor := authn.TenantIDFromContext(ctx), authn.EntityIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	var detail constitutionDetailView
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.constitutionReadProjectOn(ctx, tx, tenant, entity, actor); e != nil {
			return e
		}
		q := query.BindConstitutions(tx, m.service.names)
		row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(entity), q.ID.Eq(id)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrProjectNotFound
		}
		if e != nil {
			return e
		}
		detail = constitutionDetailView{ConstitutionView: constitutionRecord(row), Body: row.Body}
		return nil
	})
	if err != nil {
		return nil, constitutionHTTPError(ctx, err)
	}
	return &constitutionGetOutput{ETag: fmtETag(detail.Version), Body: respx.OK(ctx, detail).Body}, nil
}
func (m *Module) listConstitutionsHTTP(ctx context.Context, in *constitutionListInput) (*respx.Body[[]ConstitutionView], error) {
	entity, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, constitutionHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, constitutionHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	rows := []ConstitutionView{}
	var total int64
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.constitutionReadProjectOn(ctx, tx, tenant, entity, actor); e != nil {
			return e
		}
		q := query.BindConstitutions(tx, m.service.names)
		contract := platformquery.New(platformquery.String("state", q.State).Ops(platformquery.Eq, platformquery.In), platformquery.String("change_summary", q.ChangeSummary).Ops(platformquery.Like)).Sortable(map[string]platformquery.SortKey{"version_no": {Column: q.VersionNo}, "created_at": {Column: q.CreatedAt}, "updated_at": {Column: q.UpdatedAt}}).Reserve("offset", "limit", "sort", "lang")
		filters, e := contract.Parse(in.RawQuery)
		if e != nil {
			return e
		}
		sort := in.Sort
		if sort == "" {
			sort = "-version_no"
		}
		order, e := contract.Ordering(sort, 3, q.ID)
		if e != nil {
			return e
		}
		base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(entity))
		for _, f := range filters {
			base = base.Where(f)
		}
		total, e = base.Count()
		if e != nil {
			return e
		}
		// Document bodies are fetched only by the detail endpoint.
		found, e := base.Omit(q.Body).Order(order...).Order(q.ID).Offset(in.Offset).Limit(in.Limit).Find()
		if e != nil {
			return e
		}
		for _, row := range found {
			rows = append(rows, constitutionRecord(row))
		}
		return nil
	})
	if err != nil {
		return nil, constitutionHTTPError(ctx, err)
	}
	return respx.List(ctx, rows, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(rows), Total: total}), nil
}
