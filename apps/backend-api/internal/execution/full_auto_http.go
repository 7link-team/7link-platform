package execution

import (
	"context"
	"net/http"
	"strings"

	iamauthn "7link.com/platform/internal/iam/authn"
	transportaudit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

var _ module.HTTPMiddlewareModule = (*Module)(nil)

// Discard credential-bearing transport captures before validation, including
// malformed or unauthorized requests. The handler still receives the body.
func (m *Module) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if (len(parts) == 3 && parts[0] == "executions" && parts[2] == "full-auto-grant") || (len(parts) == 3 && parts[0] == "preview-sessions" && parts[2] == "access-grants") || (len(parts) == 1 && parts[0] == "preview-access-grants") {
			transportaudit.OmitConfidentialRequest(r.Context())
		}
		next.ServeHTTP(w, r)
	})
}

type fullAutoInput struct {
	ExecutionInput
	Cookie string `header:"Cookie" hidden:"true"`
	Body   struct {
		ReauthProof string `json:"reauth_proof" minLength:"1" maxLength:"512" writeOnly:"true"`
		Reason      string `json:"reason" minLength:"1" maxLength:"2000"`
	}
}
type fullAutoPolicyInput struct {
	ExecutionInput
	Body struct {
		ExpectedVersion int    `json:"expected_version" minimum:"0"`
		AllowFullAuto   bool   `json:"allow_full_auto"`
		Reason          string `json:"reason" minLength:"1" maxLength:"2000"`
	}
}

func (m *Module) registerFullAutoHTTP(api huma.API) error {
	iamauthn.RegisterExecutionReauthentication(api)
	op := func(id, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: http.MethodPut, Path: path, Summary: summary, Tags: []string{"executions"}, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	authz.RegisterOwned(m.registrar, api, op("execution.full_auto.grant", "/executions/{execution_id}/full-auto-grant", "Record a purpose-bound Human approval of the exact frozen full-auto plan"), "execution_id", m.owner, authz.Guard{Resource: "execution", Verb: "start", FilterData: true}, m.grantFullAutoHTTP)
	authz.RegisterOwned(m.registrar, api, op("execution.full_auto.policy", "/executions/{execution_id}/project-execution-policy", "Set the current project's full-auto execution policy"), "execution_id", m.owner, authz.Guard{Resource: "project", Verb: "manage", FilterData: true}, m.putFullAutoPolicyHTTP)
	readPolicy := op("execution.full_auto.policy.get", "/executions/{execution_id}/project-execution-policy", "Read the current project's full-auto execution policy")
	readPolicy.Method = http.MethodGet
	authz.RegisterOwned(m.registrar, api, readPolicy, "execution_id", m.owner, authz.Guard{Resource: "project", Verb: "manage", FilterData: true}, m.getFullAutoPolicyHTTP)
	for _, path := range []string{"/executions/{execution_id}/full-auto-grant", "/executions/{execution_id}/project-execution-policy"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) grantFullAutoHTTP(ctx context.Context, in *fullAutoInput) (*respx.Body[FullAutoGrant], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out FullAutoGrant
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.GrantFullAutoOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Cookie, in.Body.ReauthProof, in.Body.Reason)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) putFullAutoPolicyHTTP(ctx context.Context, in *fullAutoPolicyInput) (*respx.Body[ExecutionProjectPolicy], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out ExecutionProjectPolicy
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.PutFullAutoPolicyOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.ExpectedVersion, in.Body.AllowFullAuto, in.Body.Reason)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) getFullAutoPolicyHTTP(ctx context.Context, in *ExecutionInput) (*respx.Body[ExecutionProjectPolicy], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out ExecutionProjectPolicy
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, e := m.fullAutoExecutionOn(ctx, tx, authn.TenantIDFromContext(ctx), id, "project_manage")
		if e != nil {
			return e
		}
		out, e = m.fullAutoPolicyOn(ctx, tx, row.TenantID, row.ProjectID)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
