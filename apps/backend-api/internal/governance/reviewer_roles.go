package governance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type reviewerRolesInput struct {
	TenantID       string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
}
type reviewerRoleView struct {
	Key string  `json:"key"`
	ID  guid.ID `json:"id"`
}
type reviewerRolesOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[[]reviewerRoleView]
}

// Reviewer roles are explicitly provisioned, without changing default role
// membership or granting any Human decision authority to an Agent.
func reviewerRoleManifest() []roleManifestEntry {
	return []roleManifestEntry{
		{Key: "agent_tester", Name: "Agent tester"},
		{Key: "agent_acceptor", Name: "Agent acceptor"},
	}
}

func (m *Module) registerJReviewerRolesHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "governance.agent_reviewer_roles.provision", Method: http.MethodPost, Path: "/agent-reviewer-role-provisions", Summary: "Provision independent Agent reviewer role definitions without assigning members", Tags: []string{"tenants"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 409, 422, 500, 503}}, authz.Guard{Resource: "agent", Verb: "manage"}, m.provisionReviewerRolesHTTP)
}

func (m *Module) provisionReviewerRolesOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) ([]reviewerRoleView, error) {
	if err := m.delegationAuthorityOn(ctx, tx, tenant, 0, "agent_manage"); err != nil {
		return nil, err
	}
	manifest := reviewerRoleManifest()
	if err := m.seed.applyOn(ctx, tx, tenant, RoleManifestVersion, manifest); err != nil {
		return nil, err
	}
	out := make([]reviewerRoleView, 0, len(manifest))
	for _, entry := range manifest {
		id, err := m.roleIDOn(ctx, tx, tenant, entry.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, reviewerRoleView{Key: entry.Key, ID: id})
	}
	return out, nil
}

func (m *Module) provisionReviewerRolesHTTP(ctx context.Context, in *reviewerRolesInput) (_ *reviewerRolesOutput, err error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, delegationHTTPError(ctx, ErrInvalidArgument)
	}
	if m == nil || m.seed == nil || m.seed.acquire == nil || m.delegations.replay == nil {
		return nil, delegationHTTPError(ctx, ErrUnavailable)
	}
	k := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "governance.agent_reviewer_roles.provision", Value: in.IdempotencyKey, RequestHash: sha256.Sum256([]byte("agent_tester,agent_acceptor"))}
	if err = k.Validate(); err != nil {
		return nil, delegationHTTPError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, delegationHTTPError(ctx, err)
	}
	lease, err := m.seed.acquire(ctx, "governance:seed:"+tenant.String(), time.Minute)
	if err != nil {
		return nil, delegationHTTPError(ctx, err)
	}
	if lease == nil {
		return nil, delegationHTTPError(ctx, ErrUnavailable)
	}
	defer func() { err = errors.Join(err, lease.Unlock(context.WithoutCancel(ctx))) }()
	var result idempotency.Result
	var replayed bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lease.FenceOn(ctx, tx); e != nil {
			return e
		}
		// Recheck authority even for a cached receipt; revocation remains effective.
		if e := m.delegationAuthorityOn(ctx, tx, tenant, 0, "agent_manage"); e != nil {
			return e
		}
		var e error
		result, replayed, e = m.delegations.replay.Do(ctx, tx, k, func(work *gorm.DB) (idempotency.Result, error) {
			roles, e := m.provisionReviewerRolesOn(ctx, work, tenant)
			if e != nil {
				return idempotency.Result{}, e
			}
			body, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, roles).Body)
			return idempotency.Result{StatusCode: 201, Body: body, ResourceKind: "tenant", ResourceID: tenant}, e
		})
		return e
	})
	if err != nil {
		return nil, delegationHTTPError(ctx, err)
	}
	out := &reviewerRolesOutput{Status: result.StatusCode}
	if replayed {
		out.Replayed = "true"
	}
	if err = json.Unmarshal(result.Body, &out.Body); err != nil {
		return nil, delegationHTTPError(ctx, err)
	}
	return out, nil
}
