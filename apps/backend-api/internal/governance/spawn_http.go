package governance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	governancei18n "7link.com/platform/internal/governance/i18n"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type spawnListInput struct {
	TenantID       string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	State          string `query:"state" enum:"ACTIVE,EXHAUSTED,REVOKED,EXPIRED"`
	GranteeAgentID string `query:"grantee_agent_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Limit          int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset         int    `query:"offset" default:"0" minimum:"0"`
}
type SpawnRefInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID       string `path:"spawn_grant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type spawnMembersInput struct {
	SpawnRefInput
	Limit  int `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset int `query:"offset" default:"0" minimum:"0"`
}
type spawnCreateInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Cookie   string `header:"Cookie"`
	Body     SpawnGrantInput
}
type spawnUpdateInput struct {
	SpawnRefInput
	IfMatch string `header:"If-Match" required:"true"`
	Cookie  string `header:"Cookie"`
	Body    SpawnGrantUpdate
}
type spawnRevokeInput struct {
	SpawnRefInput
	IfMatch string `header:"If-Match" required:"true"`
	Key     string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body    struct {
		Reason string `json:"reason" minLength:"10" maxLength:"2000"`
	}
}
type spawnOutput struct {
	Status   int
	ETag     string `header:"ETag"`
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[SpawnGrantView]
}
type SpawnGrantMemberView struct {
	AgentID           guid.ID    `json:"agent_id"`
	State             string     `json:"state"`
	ReleaseReason     *string    `json:"release_reason,omitempty"`
	PerAgentCapMicros string     `json:"per_agent_cap_micros"`
	SpentMicros       string     `json:"spent_micros"`
	ReservedAt        time.Time  `json:"reserved_at"`
	ReleasedAt        *time.Time `json:"released_at,omitempty"`
}

func (m *Module) registerSpawnHTTP(api huma.API) {
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	read, manage := authz.Guard{Resource: "policy", Verb: "read"}, authz.Guard{Resource: "spawn_grant", Verb: "manage"}
	authz.Register(m.registrar, api, op("governance.spawn_grants.list", http.MethodGet, "/spawn-grants", "List bounded Agent spawn grants"), read, m.listSpawnHTTP)
	create := op("governance.spawn_grant.create", http.MethodPost, "/spawn-grants", "Grant a bounded Agent creation policy")
	create.DefaultStatus = 201
	authz.Register(m.registrar, api, create, manage, m.createSpawnHTTP)
	authz.Register(m.registrar, api, op("governance.spawn_grant.get", http.MethodGet, "/spawn-grants/{spawn_grant_id}", "Read a bounded spawn grant"), read, m.getSpawnHTTP)
	authz.Register(m.registrar, api, op("governance.spawn_grant.update", http.MethodPatch, "/spawn-grants/{spawn_grant_id}", "Update a versioned spawn grant"), manage, m.updateSpawnHTTP)
	authz.Register(m.registrar, api, op("governance.spawn_grant.revoke", http.MethodPost, "/spawn-grants/{spawn_grant_id}/revocations", "Revoke a spawn grant and release its quota"), manage, m.revokeSpawnHTTP)
	authz.Register(m.registrar, api, op("governance.spawn_grant.members", http.MethodGet, "/spawn-grants/{spawn_grant_id}/members", "Read spawn grant membership and cost snapshots"), read, m.membersSpawnPageHTTP)
}
func spawnHTTPError(ctx context.Context, e error) error {
	switch {
	case errors.Is(e, ErrSpawnScope):
		return respx.ErrStatus(ctx, 422, 160470, governancei18n.SpawnInvalid, nil)
	case errors.Is(e, ErrSpawnQuota):
		return respx.ErrStatus(ctx, 409, 160471, governancei18n.SpawnQuota, nil)
	case errors.Is(e, ErrSpawnBudget):
		return respx.ErrStatus(ctx, 409, 160472, governancei18n.SpawnBudget, nil)
	case errors.Is(e, ErrSpawnActive):
		return respx.ErrStatus(ctx, 409, 160473, governancei18n.SpawnActive, nil)
	default:
		return delegationHTTPError(ctx, e)
	}
}
func (m *Module) listSpawnHTTP(ctx context.Context, in *spawnListInput) (*respx.Body[[]SpawnGrantView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	db, e := m.writer()
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	out := []SpawnGrantView{}
	var total int64
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.authorizeDelegationReadOn(ctx, tx, tenant, 0); e != nil {
			return e
		}
		q := query.BindSpawnGrants(tx, m.seed.names)
		scope := q.WithContext(ctx).Where(q.TenantID.Eq(tenant))
		if in.State != "" {
			scope = scope.Where(q.State.Eq(in.State))
		}
		if in.GranteeAgentID != "" {
			id, e := guid.RequireID(in.GranteeAgentID)
			if e != nil {
				return ErrInvalidArgument
			}
			scope = scope.Where(q.GranteeAgentID.Eq(id))
		}
		var err error
		total, err = scope.Count()
		if err != nil {
			return err
		}
		rows, err := scope.Order(q.EndsAt, q.ID).Offset(in.Offset).Limit(in.Limit).Find()
		if err != nil {
			return err
		}
		for _, r := range rows {
			v, e := m.spawnGrantViewOn(ctx, tx, r)
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	return respx.List(ctx, out, respx.Page{Total: total, Offset: in.Offset, Limit: in.Limit}), nil
}
func (m *Module) spawnReadOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (SpawnGrantView, error) {
	if e := m.authorizeDelegationReadOn(ctx, tx, tenant, 0); e != nil {
		return SpawnGrantView{}, e
	}
	q := query.BindSpawnGrants(tx, m.seed.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return SpawnGrantView{}, ErrNotFound
	}
	if e != nil {
		return SpawnGrantView{}, e
	}
	return m.spawnGrantViewOn(ctx, tx, r)
}
func (m *Module) getSpawnHTTP(ctx context.Context, in *SpawnRefInput) (*spawnOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	db, e := m.writer()
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	var out SpawnGrantView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = m.spawnReadOn(ctx, tx, tenant, id); return e })
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	return &spawnOutput{Status: 200, ETag: "\"" + fmtVersion(out.Version) + "\"", Body: respx.OK(ctx, out).Body}, nil
}
func (m *Module) spawnHTTPCommand(ctx context.Context, tenant guid.ID, key, operation string, payload any, status int, run func(*gorm.DB) (SpawnGrantView, error)) (*spawnOutput, error) {
	db, e := m.writer()
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	if m.delegations.replay == nil {
		return nil, spawnHTTPError(ctx, ErrUnavailable)
	}
	encoded, e := json.Marshal(payload)
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	k := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: key, RequestHash: sha256.Sum256(encoded)}
	if e = k.Validate(); e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	var saved idempotency.Result
	var replayed bool
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		saved, replayed, e = m.delegations.replay.Do(ctx, tx, k, func(tx *gorm.DB) (idempotency.Result, error) {
			v, e := run(tx)
			if e != nil {
				return idempotency.Result{}, e
			}
			body, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, v).Body)
			return idempotency.Result{StatusCode: status, Body: body, ResourceKind: "spawn_grant", ResourceID: v.ID}, e
		})
		if e != nil {
			return e
		}
		return m.delegationAuthorityOn(ctx, tx, tenant, 0, "spawn_grant_manage")
	})
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	var out spawnOutput
	out.Status = saved.StatusCode
	if replayed {
		out.Replayed = "true"
	}
	if e = json.Unmarshal(saved.Body, &out.Body); e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	out.ETag = "\"" + fmtVersion(out.Body.Data.Version) + "\""
	return &out, nil
}
func (m *Module) createSpawnHTTP(ctx context.Context, in *spawnCreateInput) (*spawnOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	payload := in.Body
	payload.ReauthProof = ""
	return m.spawnHTTPCommand(ctx, tenant, in.Key, "spawn_grant.grant", payload, 201, func(tx *gorm.DB) (SpawnGrantView, error) { return m.grantSpawnOn(ctx, tx, tenant, in.Body, in.Cookie) })
}
func (m *Module) updateSpawnHTTP(ctx context.Context, in *spawnUpdateInput) (*spawnOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	version, e := parseGovernanceVersion(in.IfMatch)
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	db, e := m.writer()
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	var out SpawnGrantView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.updateSpawnGrantOn(ctx, tx, tenant, id, version, in.Body, in.Cookie)
		return e
	})
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	return &spawnOutput{Status: 200, ETag: "\"" + fmtVersion(out.Version) + "\"", Body: respx.OK(ctx, out).Body}, nil
}
func (m *Module) revokeSpawnHTTP(ctx context.Context, in *spawnRevokeInput) (*spawnOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	version, e := parseGovernanceVersion(in.IfMatch)
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	payload := struct {
		Version int
		Reason  string
	}{version, in.Body.Reason}
	return m.spawnHTTPCommand(ctx, tenant, in.Key, "spawn_grant.revoke:"+id.String(), payload, 200, func(tx *gorm.DB) (SpawnGrantView, error) {
		return m.revokeSpawnGrantOn(ctx, tx, tenant, id, version, in.Body.Reason)
	})
}
func (m *Module) membersSpawnHTTP(ctx context.Context, in *SpawnRefInput) (*respx.Body[[]SpawnGrantMemberView], error) {
	return m.membersSpawnPageHTTP(ctx, &spawnMembersInput{SpawnRefInput: *in, Limit: 100})
}
func (m *Module) membersSpawnPageHTTP(ctx context.Context, in *spawnMembersInput) (*respx.Body[[]SpawnGrantMemberView], error) {
	if in.Limit < 1 || in.Limit > 200 || in.Offset < 0 {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	db, e := m.writer()
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	out := []SpawnGrantMemberView{}
	var total int64
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.spawnReadOn(ctx, tx, tenant, id); e != nil {
			return e
		}
		q := query.BindSpawnGrantMembers(tx, m.seed.names)
		scope := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.GrantID.Eq(id))
		var e error
		if total, e = scope.Count(); e != nil {
			return e
		}
		rows, e := scope.Order(q.ReservedAt.Desc(), q.ID.Desc()).Offset(in.Offset).Limit(in.Limit).Find()
		if e != nil {
			return e
		}
		for _, r := range rows {
			out = append(out, SpawnGrantMemberView{AgentID: r.AgentID, State: r.State, ReleaseReason: r.ReleaseReason, PerAgentCapMicros: fmtInt64(r.PerAgentCapMicros), SpentMicros: fmtInt64(r.SpentMicros), ReservedAt: r.ReservedAt, ReleasedAt: r.ReleasedAt})
		}
		return nil
	})
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	return respx.List(ctx, out, respx.Page{Total: total, Offset: in.Offset, Limit: in.Limit}), nil
}
