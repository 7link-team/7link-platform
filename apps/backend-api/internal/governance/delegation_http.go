package governance

import (
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	idemi18n "7link.com/platform/internal/infra/idempotency/i18n"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

type delegationListInput struct {
	TenantID  string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ProjectID string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	State     string `query:"state" enum:"SCHEDULED,ACTIVE,REVOKED,EXPIRED"`
	Limit     int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset    int    `query:"offset" default:"0" minimum:"0"`
	BeforeID  string `query:"before_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"Exclusive descending ID boundary. Cannot be combined with a nonzero offset."`
}
type delegationRefInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID       string `path:"delegation_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type delegationGrantHTTPInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Cookie   string `header:"Cookie"`
	Body     DelegationGrantInput
}
type delegationRevokeHTTPInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID       string `path:"delegation_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body     struct {
		Reason string `json:"reason" minLength:"5" maxLength:"2000"`
	}
}
type delegationCommandOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[DelegationView]
}

func (m *Module) registerDelegationHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "governance.delegations.list", Method: http.MethodGet, Path: "/delegations", Summary: "List durable project delegations", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}, authz.Guard{Resource: "policy", Verb: "read"}, m.listDelegationsHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "governance.delegation.create", Method: http.MethodPost, Path: "/delegations", Summary: "Grant a bounded Agent delegation with current IAM carriers", Tags: []string{"tenants"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 409, 422, 500, 503}}, authz.Guard{Resource: "delegation", Verb: "grant"}, m.grantDelegationHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "governance.delegation.get", Method: http.MethodGet, Path: "/delegations/{delegation_id}", Summary: "Read a durable delegation and its current lifecycle", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, authz.Guard{Resource: "policy", Verb: "read"}, m.getDelegationHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "governance.delegation.revoke", Method: http.MethodPost, Path: "/delegations/{delegation_id}/revocations", Summary: "Revoke a delegation and remove its IAM carriers atomically", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.Guard{Resource: "delegation", Verb: "grant"}, m.revokeDelegationHTTP)
}

func (m *Module) authorizeDelegationReadOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if m.principals.LockActiveRow == nil || m.delegations.members == nil || m.explain == nil {
		return ErrUnavailable
	}
	if _, active, e := m.principals.LockActiveRow(ctx, tx, actor); e != nil {
		return e
	} else if !active {
		return ErrForbidden
	}
	if active, e := m.delegations.members.IsMemberActiveOn(ctx, tx, tenant, actor); e != nil {
		return e
	} else if !active {
		return ErrForbidden
	}
	x, e := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: actor, Permission: "policy_read"})
	if e != nil {
		return e
	}
	if !x.Allowed {
		return ErrForbidden
	}
	return nil
}

func (m *Module) listDelegationsHTTP(ctx context.Context, in *delegationListInput) (*respx.Body[[]DelegationView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	var project guid.ID
	var before guid.ID
	if in.BeforeID != "" {
		before, e = guid.RequireID(in.BeforeID)
		if e != nil || in.Offset != 0 {
			return nil, inboxHTTPError(ctx, ErrInvalidArgument)
		}
	}
	if in.ProjectID != "" {
		project, e = guid.RequireID(in.ProjectID)
		if e != nil {
			return nil, inboxHTTPError(ctx, ErrInvalidArgument)
		}
	}
	db, e := m.writer()
	if e != nil {
		return nil, inboxHTTPError(ctx, e)
	}
	items := []DelegationView{}
	var total int64
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.authorizeDelegationReadOn(ctx, tx, tenant, project); e != nil {
			return e
		}
		q := query.BindDelegations(tx, m.seed.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(tenant))
		if project > 0 {
			stmt = stmt.Where(q.ProjectID.Eq(project))
		}
		if in.State == "SCHEDULED" {
			stmt = stmt.Where(q.State.Eq("ACTIVE"), q.ActivatedAt.IsNull())
		} else if in.State == "ACTIVE" {
			stmt = stmt.Where(q.State.Eq("ACTIVE"), q.ActivatedAt.IsNotNull())
		} else if in.State != "" {
			stmt = stmt.Where(q.State.Eq(in.State))
		}
		var e error
		total, e = stmt.Count()
		if e != nil {
			return e
		}
		if before > 0 {
			stmt = stmt.Where(q.ID.Lt(before))
		}
		rows, e := stmt.Order(q.ID.Desc()).Offset(in.Offset).Limit(in.Limit).Find()
		if e != nil {
			return e
		}
		for _, r := range rows {
			v, e := delegationView(r)
			if e != nil {
				return e
			}
			items = append(items, v)
		}
		return nil
	})
	if e != nil {
		return nil, inboxHTTPError(ctx, e)
	}
	return respx.List(ctx, items, respx.Page{Total: total, Limit: in.Limit, Offset: in.Offset}), nil
}
func (m *Module) getDelegationHTTP(ctx context.Context, in *delegationRefInput) (*respx.Body[DelegationView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	db, e := m.writer()
	if e != nil {
		return nil, inboxHTTPError(ctx, e)
	}
	var out DelegationView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.authorizeDelegationReadOn(ctx, tx, tenant, 0); e != nil {
			return e
		}
		q := query.BindDelegations(tx, m.seed.names)
		r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Take()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		out, e = delegationView(r)
		if e != nil {
			return e
		}
		return m.delegationUsesOn(ctx, tx, tenant, &out)
	})
	if e != nil {
		return nil, inboxHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

func delegationHTTPError(ctx context.Context, e error) error {
	switch {
	case errors.Is(e, idempotency.ErrInvalidKey):
		return respx.ErrStatus(ctx, 422, 176422, idemi18n.InvalidKey, nil)
	case errors.Is(e, idempotency.ErrKeyReused):
		return respx.ErrStatus(ctx, 409, 176425, idemi18n.KeyReused, nil)
	case errors.Is(e, idempotency.ErrInProgress):
		return respx.ErrStatus(ctx, 409, 176409, idemi18n.InProgress, nil)
	default:
		return inboxHTTPError(ctx, e)
	}
}

func (m *Module) delegationHTTPCommand(ctx context.Context, tenant guid.ID, key, operation string, payload any, authorize func(*gorm.DB) error, run func(*gorm.DB) (DelegationView, error), status int) (*delegationCommandOutput, error) {
	db, e := m.writer()
	if e != nil {
		return nil, delegationHTTPError(ctx, e)
	}
	if m.delegations.replay == nil {
		return nil, delegationHTTPError(ctx, ErrUnavailable)
	}
	encoded, e := json.Marshal(payload)
	if e != nil {
		return nil, delegationHTTPError(ctx, e)
	}
	k := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: key, RequestHash: sha256.Sum256(encoded)}
	if e = k.Validate(); e != nil {
		return nil, delegationHTTPError(ctx, e)
	}
	var result idempotency.Result
	var replayed bool
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		result, replayed, e = m.delegations.replay.Do(ctx, tx, k, func(work *gorm.DB) (idempotency.Result, error) {
			v, e := run(work)
			if e != nil {
				return idempotency.Result{}, e
			}
			body, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, v).Body)
			return idempotency.Result{StatusCode: status, Body: body, ResourceKind: "delegation", ResourceID: v.ID}, e
		})
		if e != nil {
			return e
		}
		// Cached results never revive authority revoked since the original write.
		return authorize(tx)
	})
	if e != nil {
		return nil, delegationHTTPError(ctx, e)
	}
	var out delegationCommandOutput
	out.Status = result.StatusCode
	if replayed {
		out.Replayed = "true"
	}
	if e = json.Unmarshal(result.Body, &out.Body); e != nil {
		return nil, delegationHTTPError(ctx, e)
	}
	return &out, nil
}
func (m *Module) grantDelegationHTTP(ctx context.Context, in *delegationGrantHTTPInput) (*delegationCommandOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, delegationHTTPError(ctx, ErrInvalidArgument)
	}
	payload := in.Body
	// A new one-use proof does not change the business request after transport
	// failure. A replay still rechecks current grant authority before returning.
	payload.ReauthProof = ""
	return m.delegationHTTPCommand(ctx, tenant, in.Key, "delegation.grant", payload, func(tx *gorm.DB) error {
		return m.delegationAuthorityOn(ctx, tx, tenant, in.Body.ProjectID, "delegation_grant")
	}, func(tx *gorm.DB) (DelegationView, error) {
		return m.grantDelegationOn(ctx, tx, tenant, in.Body, in.Cookie)
	}, 201)
}
func (m *Module) revokeDelegationHTTP(ctx context.Context, in *delegationRevokeHTTPInput) (*delegationCommandOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, delegationHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, delegationHTTPError(ctx, ErrInvalidArgument)
	}
	return m.delegationHTTPCommand(ctx, tenant, in.Key, "delegation.revoke:"+id.String(), in.Body, func(tx *gorm.DB) error {
		q := query.BindDelegations(tx, m.seed.names)
		r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Take()
		if e != nil {
			return e
		}
		return m.delegationAuthorityOn(ctx, tx, tenant, r.ProjectID, "delegation_grant")
	}, func(tx *gorm.DB) (DelegationView, error) {
		return m.revokeDelegationOn(ctx, tx, tenant, id, in.Body.Reason)
	}, 200)
}
