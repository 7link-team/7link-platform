package planning

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	pi "7link.com/platform/internal/planning/i18n"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"strings"
)

type LoopPath struct {
	ID string `path:"loop_contract_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type LoopListInput struct {
	PlanningTenantInput
	Cursor  string `query:"cursor" maxLength:"128"`
	Limit   int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Sort    string `query:"sort" enum:"id" default:"id"`
	AgentID string `query:"agent_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type LoopContractPage struct {
	Items      []LoopContractView `json:"items"`
	HasMore    bool               `json:"has_more"`
	NextCursor string             `json:"next_cursor,omitempty"`
}
type LoopRunPage struct {
	Items      []LoopRunView `json:"items"`
	HasMore    bool          `json:"has_more"`
	NextCursor string        `json:"next_cursor,omitempty"`
}
type LoopRunListInput struct {
	LoopPath
	Cursor string `query:"cursor" maxLength:"128"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Sort   string `query:"sort" enum:"id" default:"id"`
}
type LoopCreateInput struct {
	PlanningTenantInput
	Key  string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body struct {
		LoopDefinition
		Activate bool `json:"activate"`
	}
}
type LoopStateInput struct {
	LoopPath
	Key     string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	IfMatch string `header:"If-Match"`
	Body    struct {
		State string `json:"state" enum:"ACTIVE,PAUSED,ARCHIVED"`
	}
}
type LoopCommandOutput struct {
	Status   int
	ETag     string `header:"ETag"`
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[LoopContractView]
}
type LoopReadOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[LoopContractView]
}
type LoopPatchInput struct {
	LoopPath
	Key     string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	IfMatch string `header:"If-Match"`
	Body    LoopPatch
}
type LoopFindingRef struct {
	Kind string  `json:"kind" maxLength:"64"`
	ID   guid.ID `json:"id"`
}
type LoopFinding struct {
	Title    string          `json:"title" maxLength:"200"`
	Severity string          `json:"severity" enum:"INFO,WARN,FAIL"`
	Ref      *LoopFindingRef `json:"ref,omitempty"`
}
type LoopResultInput struct {
	ID   string `path:"loop_run_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key  string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body struct {
		ReportID         *guid.ID `json:"report_id,omitempty"`
		SelfCheckResults []struct {
			Key    string `json:"key"`
			Passed bool   `json:"passed"`
			Note   string `json:"note,omitempty"`
		} `json:"self_check_results"`
		CheckResult *struct {
			Status   string        `json:"status" enum:"OK,WARN,FAIL"`
			Findings []LoopFinding `json:"findings,omitempty" maxItems:"50"`
		} `json:"check_result,omitempty"`
		Note string `json:"note,omitempty" maxLength:"2000"`
	}
}

func loopCursor(scope string, id guid.ID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(scope + ":" + id.String()))
}
func parseLoopCursor(scope, cursor string) (guid.ID, error) {
	if cursor == "" {
		return 0, nil
	}
	b, e := base64.RawURLEncoding.DecodeString(cursor)
	if e != nil || !strings.HasPrefix(string(b), scope+":") {
		return 0, ErrInvalid
	}
	id, e := guid.RequireID(strings.TrimPrefix(string(b), scope+":"))
	if e != nil {
		return 0, ErrInvalid
	}
	return id, nil
}
func loopOperation(id, method, path, tag, summary string, status int) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{tag}, Summary: summary, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 428, 500, 503}}
}
func (m *Module) registerLoopHTTP(api huma.API) {
	authz.RegisterOwnedTenantMember(m.registrar, api, loopOperation("planning.loop.patch", "PATCH", "/loop-contracts/{loop_contract_id}", "loop-contracts", "Update a loop contract definition", 200), "loop_contract_id", m.loopOwner, m.patchLoopHTTP)
	authz.RegisterTenantMember(m.registrar, api, loopOperation("planning.loop.list", "GET", "/loop-contracts", "tenants", "List visible loop contracts", 200), m.listLoopsHTTP)
	authz.RegisterTenantMember(m.registrar, api, loopOperation("planning.loop.create", "POST", "/loop-contracts", "tenants", "Create an Agent loop contract", 201), m.createLoopHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, loopOperation("planning.loop.get", "GET", "/loop-contracts/{loop_contract_id}", "loop-contracts", "Read a visible loop contract", 200), "loop_contract_id", m.loopOwner, m.getLoopHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, loopOperation("planning.loop.state", "PUT", "/loop-contracts/{loop_contract_id}/state", "loop-contracts", "Change a loop contract state", 200), "loop_contract_id", m.loopOwner, m.stateLoopHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, loopOperation("planning.loop.runs", "GET", "/loop-contracts/{loop_contract_id}/runs", "loop-contracts", "List actual reminder-triggered loop runs", 200), "loop_contract_id", m.loopOwner, m.loopRunsHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, loopOperation("planning.loop.result", "POST", "/loop-runs/{loop_run_id}/results", "loop-runs", "Submit a loop result when required evidence capabilities are available", 200), "loop_run_id", m.loopRunOwner, m.loopResultHTTP)
}
func (m *Module) patchLoopHTTP(ctx context.Context, in *LoopPatchInput) (*LoopCommandOutput, error) {
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	if in.IfMatch == "" {
		return nil, respx.ErrStatus(ctx, 428, 180428, pi.Invalid, nil)
	}
	version, e := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if e != nil || version < 1 || in.IfMatch != strconv.Quote(strconv.Itoa(version)) {
		return nil, planningError(ctx, ErrInvalid)
	}
	return m.loopCommand(ctx, authn.TenantIDFromContext(ctx), in.Key, "planning.loop.patch:"+id.String(), struct {
		Version int
		Patch   LoopPatch
	}{version, in.Body}, func(tx *gorm.DB) (LoopContractView, error) {
		return m.patchLoopOn(ctx, tx, authn.TenantIDFromContext(ctx), id, version, in.Body)
	})
}
func (m *Module) loopOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, e := m.database()
	if e != nil {
		return authz.ResourceOwner{}, false, e
	}
	q := query.BindLoopContracts(db, m.names)
	r, e := q.WithContext(ctx).Select(q.TenantID).Where(q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if e != nil {
		return authz.ResourceOwner{}, false, e
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: r.TenantID}, true, nil
}
func (m *Module) loopRunOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, e := m.database()
	if e != nil {
		return authz.ResourceOwner{}, false, e
	}
	q := query.BindLoopRuns(db, m.names)
	r, e := q.WithContext(ctx).Select(q.TenantID).Where(q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if e != nil {
		return authz.ResourceOwner{}, false, e
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: r.TenantID}, true, nil
}
func (m *Module) loopCommand(ctx context.Context, tenant guid.ID, key, operation string, body any, fn func(*gorm.DB) (LoopContractView, error)) (*LoopCommandOutput, error) {
	db, e := m.database()
	if e != nil {
		return nil, planningError(ctx, e)
	}
	if m.replay == nil {
		return nil, planningError(ctx, ErrUnavailable)
	}
	hash, e := idempotency.HashRequest(http.MethodPost, operation, map[string]string{"tenant_id": tenant.String()}, body)
	if e != nil {
		return nil, planningError(ctx, e)
	}
	k := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: key, RequestHash: hash}
	if e = k.Validate(); e != nil {
		return nil, planningError(ctx, e)
	}
	var view LoopContractView
	replayed := false
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result, again, e := m.replay.Do(ctx, tx, k, func(tx *gorm.DB) (idempotency.Result, error) {
			v, e := fn(tx)
			if e != nil {
				return idempotency.Result{}, e
			}
			b, e := json.Marshal(v)
			return idempotency.Result{StatusCode: 200, Body: b, ResourceID: v.ID, ResourceKind: "loop_contract"}, e
		})
		if e != nil {
			return e
		}
		replayed = again
		if e = json.Unmarshal(result.Body, &view); e != nil {
			return e
		}
		r, e := m.commandLoopOn(ctx, tx, tenant, view.ID)
		if e != nil {
			return e
		}
		return m.loopAuthority(ctx, tx, tenant, r, true)
	})
	if e != nil {
		return nil, planningError(ctx, e)
	}
	out := &LoopCommandOutput{Status: 200, ETag: strconv.Quote(strconv.Itoa(view.Version)), Body: respx.OK(ctx, view).Body}
	if replayed {
		out.Replayed = "true"
	}
	return out, nil
}
func (m *Module) createLoopHTTP(ctx context.Context, in *LoopCreateInput) (*LoopCommandOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	out, e := m.loopCommand(ctx, tenant, in.Key, "planning.loop.create", in.Body, func(tx *gorm.DB) (LoopContractView, error) {
		if in.Body.Activate && !m.loopRuntimeAvailable() {
			return LoopContractView{}, ErrUnavailable
		}
		return m.createLoopOn(ctx, tx, tenant, in.Body.LoopDefinition, in.Body.Activate)
	})
	if out != nil {
		out.Status = 201
	}
	return out, e
}
func (m *Module) stateLoopHTTP(ctx context.Context, in *LoopStateInput) (*LoopCommandOutput, error) {
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	if in.IfMatch == "" {
		return nil, respx.ErrStatus(ctx, 428, 180428, pi.Invalid, nil)
	}
	raw := strings.Trim(in.IfMatch, "\"")
	version, e := strconv.Atoi(raw)
	if e != nil || version < 1 || in.IfMatch != strconv.Quote(strconv.Itoa(version)) {
		return nil, planningError(ctx, ErrInvalid)
	}
	return m.loopCommand(ctx, authn.TenantIDFromContext(ctx), in.Key, "planning.loop.state:"+id.String(), struct {
		Version int
		State   string
	}{version, in.Body.State}, func(tx *gorm.DB) (LoopContractView, error) {
		if in.Body.State == "ACTIVE" && !m.loopRuntimeAvailable() {
			current, err := m.commandLoopOn(ctx, tx, authn.TenantIDFromContext(ctx), id)
			if err != nil {
				return LoopContractView{}, err
			}
			if current.Version != version {
				return LoopContractView{}, ErrConflict
			}
			return LoopContractView{}, ErrUnavailable
		}
		return m.changeLoopStateOn(ctx, tx, authn.TenantIDFromContext(ctx), id, version, in.Body.State)
	})
}
func (m *Module) getLoopHTTP(ctx context.Context, in *LoopPath) (*LoopReadOutput, error) {
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	db, e := m.database()
	if e != nil {
		return nil, planningError(ctx, e)
	}
	var view LoopContractView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := m.commandLoopOn(ctx, tx, authn.TenantIDFromContext(ctx), id, false)
		if e != nil {
			return e
		}
		if e = m.loopAuthority(ctx, tx, r.TenantID, r, false); e != nil {
			if errors.Is(e, ErrForbidden) {
				return ErrNotFound
			}
			return e
		}
		view, e = m.availableLoopView(r)
		return e
	})
	if e != nil {
		return nil, planningError(ctx, e)
	}
	return &LoopReadOutput{ETag: strconv.Quote(strconv.Itoa(view.Version)), Body: respx.OK(ctx, view).Body}, nil
}
func (m *Module) listLoopsHTTP(ctx context.Context, in *LoopListInput) (*respx.Body[LoopContractPage], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	if in.Limit < 1 || in.Limit > 100 {
		return nil, planningError(ctx, ErrInvalid)
	}
	actor := authn.PrincipalIDFromContext(ctx)
	scope := "loops:" + tenant.String() + ":" + actor.String() + ":" + in.AgentID
	after, e := parseLoopCursor(scope, in.Cursor)
	if e != nil {
		return nil, planningError(ctx, e)
	}
	db, e := m.database()
	if e != nil {
		return nil, planningError(ctx, e)
	}
	page := LoopContractPage{Items: []LoopContractView{}}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Validate current identity even for empty collections before evaluating scope.
		if e := m.loopAuthority(ctx, tx, tenant, &model.LoopContract{OwnerPrincipalID: actor, AgentID: actor}, false); e != nil {
			return e
		}
		q := query.BindLoopContracts(tx, m.names)
		rows := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(after))
		c, e := m.policy(ctx, tx, tenant, "report_read", actor)
		if e != nil {
			return e
		}
		if !c.AllowAll {
			rows = rows.Where(q.WithContext(ctx).Where(q.OwnerPrincipalID.Eq(actor)).Or(q.AgentID.Eq(actor)))
		}
		if in.AgentID != "" {
			agent, e := guid.RequireID(in.AgentID)
			if e != nil {
				return ErrInvalid
			}
			rows = rows.Where(q.AgentID.Eq(agent))
		}
		found, e := rows.Order(q.ID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		if len(found) > in.Limit {
			page.HasMore = true
			found = found[:in.Limit]
			page.NextCursor = loopCursor(scope, found[len(found)-1].ID)
		}
		for _, r := range found {
			if e = m.loopAuthority(ctx, tx, tenant, r, false); e != nil {
				return e
			}
			v, e := m.availableLoopView(r)
			if e != nil {
				return e
			}
			page.Items = append(page.Items, v)
		}
		return nil
	})
	if e != nil {
		return nil, planningError(ctx, e)
	}
	return respx.OK(ctx, page), nil
}
func (m *Module) loopRunsHTTP(ctx context.Context, in *LoopRunListInput) (*respx.Body[LoopRunPage], error) {
	id, e := guid.RequireID(in.ID)
	if e != nil || in.Limit < 1 || in.Limit > 100 {
		return nil, planningError(ctx, ErrInvalid)
	}
	tenant := authn.TenantIDFromContext(ctx)
	scope := "runs:" + tenant.String() + ":" + id.String()
	after, e := parseLoopCursor(scope, in.Cursor)
	if e != nil {
		return nil, planningError(ctx, e)
	}
	db, e := m.database()
	if e != nil {
		return nil, planningError(ctx, e)
	}
	page := LoopRunPage{Items: []LoopRunView{}}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := m.commandLoopOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		if e = m.loopAuthority(ctx, tx, tenant, r, false); e != nil {
			return ErrNotFound
		}
		q := query.BindLoopRuns(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.LoopContractID.Eq(id), q.ID.Gt(after)).Order(q.ID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > in.Limit {
			page.HasMore = true
			rows = rows[:in.Limit]
			page.NextCursor = loopCursor(scope, rows[len(rows)-1].ID)
		}
		for _, row := range rows {
			page.Items = append(page.Items, loopRunView(row))
		}
		return nil
	})
	if e != nil {
		return nil, planningError(ctx, e)
	}
	return respx.OK(ctx, page), nil
}
func (m *Module) loopResultHTTP(ctx context.Context, in *LoopResultInput) (*respx.Body[LoopRunView], error) {
	return m.recordLoopResultHTTP(ctx, in)
}
