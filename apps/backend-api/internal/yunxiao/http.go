package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	yi18n "7link.com/platform/internal/yunxiao/i18n"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"strconv"
	"strings"
)

type TenantInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type ConnectionPath struct {
	TenantInput
	ConnectionID string `path:"connection_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type MappingPath struct {
	ConnectionPath
	MappingID string `path:"mapping_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type WriteHeaders struct {
	IdempotencyKey string `header:"Idempotency-Key" minLength:"1" maxLength:"64" required:"true"`
	IfMatch        string `header:"If-Match" doc:"Quoted aggregate version; required for updates."`
	Cookie         string `header:"Cookie" hidden:"true"`
}
type createConnectionInput struct {
	TenantInput
	WriteHeaders
	Body ConnectionInput
}
type updateConnectionInput struct {
	ConnectionPath
	WriteHeaders
	Body ConnectionInput
}
type createMappingInput struct {
	ConnectionPath
	WriteHeaders
	Body MappingInput
}
type updateMappingInput struct {
	MappingPath
	WriteHeaders
	Body MappingInput
}
type commandOutput[T any] struct {
	Status   int
	ETag     string `header:"ETag"`
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[T]
}
type ListInput struct {
	TenantInput
	Cursor string `query:"cursor" maxLength:"200"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Sort   string `query:"sort" default:"id" enum:"id"`
}
type mappingListInput struct {
	ConnectionPath
	Cursor string `query:"cursor" maxLength:"200"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Sort   string `query:"sort" default:"id" enum:"id"`
}
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.registrar == nil {
		return ErrUnavailable
	}
	guard := authz.Guard{Resource: "integration", Verb: "manage", FilterData: true}
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"integration-connections"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 412, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("integration.connection.list", "GET", "/integration-connections", "List integration connections", 200), guard, m.listConnectionsHTTP)
	authz.Register(m.registrar, api, op("integration.connection.create", "POST", "/integration-connections", "Configure an integration connection", 201), guard, m.createConnectionHTTP)
	authz.Register(m.registrar, api, op("integration.connection.update", "PATCH", "/integration-connections/{connection_id}", "Update integration credentials", 200), guard, m.updateConnectionHTTP)
	authz.Register(m.registrar, api, op("integration.mapping.list", "GET", "/integration-connections/{connection_id}/project-mappings", "List project mappings", 200), guard, m.listMappingsHTTP)
	authz.Register(m.registrar, api, op("integration.mapping.create", "POST", "/integration-connections/{connection_id}/project-mappings", "Map a project", 201), guard, m.createMappingHTTP)
	authz.Register(m.registrar, api, op("integration.mapping.update", "PATCH", "/integration-connections/{connection_id}/project-mappings/{mapping_id}", "Update project synchronization policy", 200), guard, m.updateMappingHTTP)
	authz.Register(m.registrar, api, op("integration.connection.check", "POST", "/integration-connections/{connection_id}/checks", "Check verified remote capabilities", 200), guard, m.remoteConnectionHTTP)
	authz.Register(m.registrar, api, op("integration.external-project.list", "GET", "/integration-connections/{connection_id}/external-projects", "Read remote projects", 200), guard, m.remoteConnectionHTTP)
	authz.Register(m.registrar, api, op("integration.import.create", "POST", "/integration-connections/{connection_id}/project-mappings/{mapping_id}/import-jobs", "Preview remote import", 201), guard, m.remoteMappingHTTP)
	authz.Register(m.registrar, api, op("integration.state-mapping.list", "GET", "/integration-connections/{connection_id}/project-mappings/{mapping_id}/state-mappings", "Read verified state mappings", 200), guard, m.remoteMappingHTTP)
	authz.Register(m.registrar, api, op("integration.state-mapping.replace", "PUT", "/integration-connections/{connection_id}/project-mappings/{mapping_id}/state-mappings", "Confirm verified external state mappings", 200), guard, m.remoteMappingHTTP)
	for _, path := range []string{"/tenants/{tenant_id}/integration-connections", "/tenants/{tenant_id}/integration-connections/{connection_id}", "/tenants/{tenant_id}/integration-connections/{connection_id}/project-mappings", "/tenants/{tenant_id}/integration-connections/{connection_id}/project-mappings/{mapping_id}"} {
		if e := authz.RegisterRequestContract(api, path); e != nil {
			return e
		}
	}
	if e := m.registerIntakeChannelsHTTP(api); e != nil {
		return e
	}
	if err := m.registerPlanningHTTP(api); err != nil {
		return err
	}
	if err := m.registerAutomationRulesHTTP(api); err != nil {
		return err
	}
	m.registerIngressHTTP(api)
	return m.registerWorkHTTP(api)
}
func (m *Module) database() *gorm.DB {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil {
		return nil
	}
	return m.runtime.Database().Write()
}
func versionHeader(s string) (int, error) {
	if len(s) < 3 || s[0] != '"' || s[len(s)-1] != '"' {
		return 0, ErrConflict
	}
	n, e := strconv.Atoi(s[1 : len(s)-1])
	if e != nil || n <= 0 {
		return 0, ErrConflict
	}
	return n, nil
}
func httpError(ctx context.Context, e error) error {
	if errors.Is(e, ErrDefectFloor) {
		return respx.ErrStatus(ctx, 422, 126002, yi18n.Invalid, map[string]any{"reason_key": "defect_floor"})
	}
	if respx.IsStatusError(e) {
		return e
	}
	status, code, key := 500, 190500, respx.InternalServerError
	switch {
	case errors.Is(e, ErrIngressSignature):
		status, code, key = 401, 190021, yi18n.IngressSignature
	case errors.Is(e, ErrVersionLocked):
		status, code, key = 409, 126030, yi18n.Conflict
	case errors.Is(e, ErrIterationClosed):
		status, code, key = 409, 126031, yi18n.Conflict
	case errors.Is(e, ErrUnavailable), errors.Is(e, idempotency.ErrUnavailable), errors.Is(e, authn.ErrUnavailable), errors.Is(e, core.ErrUnavailable), errors.Is(e, delivery.ErrUnavailable):
		status, code, key = 503, 190503, yi18n.Unavailable
	case errors.Is(e, ErrForbidden), errors.Is(e, delivery.ErrWorkForbidden), errors.Is(e, authn.ErrInvalidCredentials), errors.Is(e, authn.ErrInvalidSession), errors.Is(e, authn.ErrSessionNotFound), errors.Is(e, delivery.ErrConstitutionHumanOnly):
		status, code, key = 403, 190403, yi18n.Forbidden
	case errors.Is(e, ErrNotFound), errors.Is(e, delivery.ErrProjectNotFound):
		status, code, key = 404, 190404, yi18n.NotFound
	case errors.Is(e, ErrConflict), errors.Is(e, gorm.ErrDuplicatedKey), errors.Is(e, core.ErrAuthorizationChanged), errors.Is(e, delivery.ErrWorkProjectReadOnly):
		status, code, key = 409, 190409, yi18n.Conflict
	case errors.Is(e, ErrInvalid), errors.Is(e, idempotency.ErrInvalidKey), errors.Is(e, idempotency.ErrKeyReused):
		status, code, key = 422, 190400, yi18n.Invalid
	case errors.Is(e, idempotency.ErrInProgress):
		status, code, key = 409, 190409, yi18n.Conflict
	}
	respx.ReportCause(ctx, status, e)
	return respx.ErrStatus(ctx, status, code, key, nil)
}
func command[T any](m *Module, ctx context.Context, tenant guid.ID, headers WriteHeaders, operation string, body any, status int, fn func(*gorm.DB) (T, int, error)) (*commandOutput[T], error) {
	return commandAuthorized(m, ctx, tenant, headers, operation, body, status, func(tx *gorm.DB) error { return m.authorityOn(ctx, tx, tenant) }, fn)
}
func commandAuthorized[T any](m *Module, ctx context.Context, tenant guid.ID, headers WriteHeaders, operation string, body any, status int, authorize func(*gorm.DB) error, fn func(*gorm.DB) (T, int, error)) (*commandOutput[T], error) {
	db := m.database()
	if db == nil || m.idem == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	hash, e := idempotency.HashRequest("COMMAND", operation, map[string]string{"tenant": tenant.String(), "if_match": headers.IfMatch}, body)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: headers.IdempotencyKey, RequestHash: hash}
	if e = key.Validate(); e != nil {
		return nil, httpError(ctx, e)
	}
	var result idempotency.Result
	var replayed bool
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Replays recheck current Human identity and IAM authority before returning data.
		if err := authorize(tx); err != nil {
			return err
		}
		var err error
		result, replayed, err = m.idem.Do(ctx, tx, key, func(work *gorm.DB) (idempotency.Result, error) {
			v, version, err := fn(work)
			if err != nil {
				return idempotency.Result{}, err
			}
			data, err := json.Marshal(v)
			if err != nil {
				return idempotency.Result{}, err
			}
			stored, err := json.Marshal(struct {
				Data    json.RawMessage `json:"data"`
				Version int             `json:"version"`
			}{data, version})
			return idempotency.Result{StatusCode: status, Body: stored}, err
		})
		return err
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	var stored struct {
		Data    json.RawMessage `json:"data"`
		Version int             `json:"version"`
	}
	if e = json.Unmarshal(result.Body, &stored); e != nil {
		return nil, httpError(ctx, e)
	}
	var data T
	if e = json.Unmarshal(stored.Data, &data); e != nil {
		return nil, httpError(ctx, e)
	}
	out := &commandOutput[T]{Status: result.StatusCode, ETag: strconv.Quote(strconv.Itoa(stored.Version)), Body: respx.OK(ctx, data).Body}
	if replayed {
		out.Replayed = "true"
	}
	return out, nil
}
func (m *Module) createConnectionHTTP(ctx context.Context, in *createConnectionInput) (*commandOutput[ConnectionView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return command(m, ctx, tenant, in.WriteHeaders, "integration.connection.create", in.Body, 201, func(tx *gorm.DB) (ConnectionView, int, error) {
		v, e := m.saveConnectionOn(ctx, tx, tenant, 0, 0, in.Body, in.Cookie)
		return v, v.Version, e
	})
}
func (m *Module) updateConnectionHTTP(ctx context.Context, in *updateConnectionInput) (*commandOutput[ConnectionView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ConnectionID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	version, e := versionHeader(in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return command(m, ctx, tenant, in.WriteHeaders, "integration.connection.update:"+id.String(), in.Body, 200, func(tx *gorm.DB) (ConnectionView, int, error) {
		v, e := m.saveConnectionOn(ctx, tx, tenant, id, version, in.Body, in.Cookie)
		return v, v.Version, e
	})
}
func (m *Module) createMappingHTTP(ctx context.Context, in *createMappingInput) (*commandOutput[MappingView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	connection, e := guid.RequireID(in.ConnectionID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return command(m, ctx, tenant, in.WriteHeaders, "integration.mapping.create:"+connection.String(), in.Body, 201, func(tx *gorm.DB) (MappingView, int, error) {
		v, e := m.saveMappingOn(ctx, tx, tenant, connection, 0, 0, in.Body)
		return v, v.Version, e
	})
}
func (m *Module) updateMappingHTTP(ctx context.Context, in *updateMappingInput) (*commandOutput[MappingView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	connection, e := guid.RequireID(in.ConnectionID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.MappingID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	version, e := versionHeader(in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return command(m, ctx, tenant, in.WriteHeaders, "integration.mapping.update:"+id.String(), in.Body, 200, func(tx *gorm.DB) (MappingView, int, error) {
		v, e := m.saveMappingOn(ctx, tx, tenant, connection, id, version, in.Body)
		return v, v.Version, e
	})
}
func pageCursor(tenant, parent, last guid.ID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(tenant.String() + ":" + parent.String() + ":" + last.String()))
}
func parseCursor(s string, tenant, parent guid.ID) (guid.ID, error) {
	if s == "" {
		return 0, nil
	}
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil {
		return 0, ErrInvalid
	}
	prefix := tenant.String() + ":" + parent.String() + ":"
	if !strings.HasPrefix(string(b), prefix) {
		return 0, ErrInvalid
	}
	n, e := guid.RequireID(strings.TrimPrefix(string(b), prefix))
	if e != nil {
		return 0, ErrInvalid
	}
	return n, nil
}
func (m *Module) listConnectionsHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[ConnectionView]], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	last, e := parseCursor(in.Cursor, tenant, 0)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	if in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalid)
	}
	db := m.database()
	if db == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	out := Page[ConnectionView]{Items: []ConnectionView{}}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.authorityOn(ctx, tx, tenant); e != nil {
			return e
		}
		q := query.BindConnections(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(last)).Order(q.ID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
			out.NextCursor = pageCursor(tenant, 0, rows[len(rows)-1].ID)
		}
		for _, r := range rows {
			out.Items = append(out.Items, connectionView(r))
		}
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) listMappingsHTTP(ctx context.Context, in *mappingListInput) (*respx.Body[Page[MappingView]], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	connection, e := guid.RequireID(in.ConnectionID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	last, e := parseCursor(in.Cursor, tenant, connection)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	if in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalid)
	}
	db := m.database()
	if db == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	out := Page[MappingView]{Items: []MappingView{}}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.authorityOn(ctx, tx, tenant); e != nil {
			return e
		}
		if _, e := m.connectionOn(ctx, tx, tenant, connection); e != nil {
			return e
		}
		q := query.BindProjectMappings(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ConnectionID.Eq(connection), q.ID.Gt(last)).Order(q.ID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
			out.NextCursor = pageCursor(tenant, connection, rows[len(rows)-1].ID)
		}
		for _, r := range rows {
			out.Items = append(out.Items, mappingView(r))
		}
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

type RemoteCapabilityView struct {
	Availability string `json:"availability" enum:"UNAVAILABLE"`
}

func (m *Module) remoteConnectionHTTP(ctx context.Context, in *ConnectionPath) (*respx.Body[RemoteCapabilityView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ConnectionID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	db := m.database()
	if db == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.authorityOn(ctx, tx, tenant); e != nil {
			return e
		}
		_, e := m.connectionOn(ctx, tx, tenant, id)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return nil, httpError(ctx, ErrUnavailable)
}
func (m *Module) remoteMappingHTTP(ctx context.Context, in *MappingPath) (*respx.Body[RemoteCapabilityView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	connection, e := guid.RequireID(in.ConnectionID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.MappingID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	db := m.database()
	if db == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.authorityOn(ctx, tx, tenant); e != nil {
			return e
		}
		r, e := m.mappingOn(ctx, tx, tenant, id)
		if e != nil {
			return e
		}
		if r.ConnectionID != connection {
			return ErrNotFound
		}
		if m.projects.AuthorizeOn == nil {
			return ErrUnavailable
		}
		return m.projects.AuthorizeOn(ctx, tx, tenant, r.ProjectID, "project_manage")
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return nil, httpError(ctx, ErrUnavailable)
}
