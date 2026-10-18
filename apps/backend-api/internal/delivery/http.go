package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"7link.com/platform/internal/collab"
	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	idemi18n "7link.com/platform/internal/infra/idempotency/i18n"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const projectCreateOperation = "project.create"
const projectCreatePath = "/tenants/{tenant_id}/projects"

var rollbackProjectResponse = errors.New("delivery: rollback unsuccessful HTTP command")

type projectCreateInput struct {
	TenantID       string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" doc:"Optional request key, 1–64 printable ASCII characters without spaces; replay window 24 hours."`
	Body           projectCreateBody
}
type projectCreateOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[projectCreateView]
}

func (m *Module) RegisterHTTP(ctx context.Context, api huma.API) error {
	if m == nil || m.registrar == nil || m.entityOwner == nil {
		return ErrUnavailable
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		slog.WarnContext(ctx, "project creation unavailable; configure a writable database")
	}
	authz.Register(m.registrar, api, huma.Operation{OperationID: "project.list", Method: http.MethodGet, Path: "/projects", Summary: "List projects with activity counts", Tags: []string{"tenants"}, Errors: []int{401, 403, 422, 500, 503}}, authz.Guard{Resource: "project", Verb: "read", FilterData: true}, m.listProjectsHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: projectCreateOperation, Method: http.MethodPost, Path: "/projects", Summary: "Create a project", Tags: []string{"tenants"}, DefaultStatus: http.StatusCreated, Errors: []int{400, 401, 403, 409, 422, 500, 503}}, authz.Guard{Resource: "project", Verb: "manage", CreationAttributes: map[string]string{"environment": "DEFAULT", "project_mode": "HYBRID"}}, m.createProjectHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.get", Method: http.MethodGet, Path: "/entities/{entity_id}/project", Summary: "Get a project", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "project", Verb: "read"}, m.getProjectHTTP)
	return authz.RegisterRequestContract(api, projectCreatePath)
}

func (m *Module) createProjectHTTP(ctx context.Context, in *projectCreateInput) (*projectCreateOutput, error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, projectHTTPError(ctx, ErrInvalidArgument)
	}
	principal := authn.PrincipalIDFromContext(ctx)
	if m.runtime == nil || m.runtime.Database() == nil {
		return nil, projectHTTPError(ctx, ErrUnavailable)
	}
	db := m.runtime.Database().Write()
	if db == nil {
		return nil, projectHTTPError(ctx, ErrUnavailable)
	}
	hash, err := idempotency.HashRequest(http.MethodPost, projectCreatePath, map[string]string{"tenant_id": tenant.String()}, in.Body)
	if err != nil {
		return nil, projectHTTPError(ctx, err)
	}
	key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: principal, Operation: projectCreateOperation, Value: in.IdempotencyKey, RequestHash: hash}
	if in.IdempotencyKey != "" {
		if err := key.Validate(); err != nil {
			return nil, projectHTTPError(ctx, err)
		}
		if m.idempotency == nil {
			return nil, projectHTTPError(ctx, ErrUnavailable)
		}
	}
	ids, preparedErr := m.prepareProjectCreation(ctx, db, tenant, principal, in.Body)
	execute := func(tx *gorm.DB) (idempotency.Result, error) {
		var created projectCreated
		err := preparedErr
		if err == nil {
			created, err = m.createProjectOn(ctx, tx, tenant, principal, in.Body, collab.ProvisioningAudit{}, ids)
		}
		if err != nil {
			return projectErrorResult(ctx, err)
		}
		view, err := projectCreatedView(created)
		if err != nil {
			return projectErrorResult(ctx, err)
		}
		body, err := respx.MarshalEnvelope(ctx, respx.OK(ctx, view).Body)
		return idempotency.Result{StatusCode: http.StatusCreated, Body: body, ResourceKind: "project", ResourceID: created.project.ProjectID}, err
	}
	var response idempotency.Result
	var replayed bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		if key.Value != "" {
			response, replayed, err = m.idempotency.Do(ctx, tx, key, execute)
			return err
		}
		response, err = execute(tx)
		if err != nil {
			return err
		}
		if response.StatusCode >= 400 {
			return rollbackProjectResponse
		}
		return nil
	})
	if err != nil && !errors.Is(err, rollbackProjectResponse) {
		return nil, projectHTTPError(ctx, err)
	}
	if response.StatusCode >= 400 {
		failure, err := respx.RestoreError(response.StatusCode, response.Body)
		if err != nil {
			return nil, projectHTTPError(ctx, err)
		}
		if replayed {
			return nil, huma.ErrorWithHeaders(failure, http.Header{"Idempotent-Replayed": {"true"}})
		}
		return nil, failure
	}
	var envelope respx.Envelope[projectCreateView]
	if len(response.Body) == 0 {
		return nil, projectHTTPError(ctx, ErrUnavailable)
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil {
		return nil, projectHTTPError(ctx, err)
	}
	out := &projectCreateOutput{Status: response.StatusCode, Body: envelope}
	if replayed {
		out.Replayed = "true"
	}
	return out, nil
}

func projectErrorResult(ctx context.Context, err error) (idempotency.Result, error) {
	mapped := projectHTTPError(ctx, err)
	var status huma.StatusError
	if !errors.As(mapped, &status) {
		return idempotency.Result{}, mapped
	}
	body, encodeErr := respx.MarshalEnvelope(ctx, mapped)
	return idempotency.Result{StatusCode: status.GetStatus(), Body: body}, encodeErr
}

func projectHTTPError(ctx context.Context, err error) error {
	if respx.IsStatusError(err) {
		return err
	}
	status, code, key := http.StatusInternalServerError, 120500, respx.InternalServerError
	var data any
	switch {
	case errors.Is(err, idempotency.ErrInvalidKey):
		status, code, key = 400, 176400, idemi18n.InvalidKey
	case errors.Is(err, idempotency.ErrKeyReused):
		status, code, key = 422, 176425, idemi18n.KeyReused
	case errors.Is(err, idempotency.ErrInProgress):
		return huma.ErrorWithHeaders(respx.ErrStatus(ctx, 409, 176409, idemi18n.InProgress, nil), http.Header{"Retry-After": {"1"}})
	case errors.Is(err, ErrProjectCodeTaken):
		status, code, key = 409, 127001, deliveryi18n.ProjectCodeTaken
	case errors.Is(err, core.ErrEntityConflict):
		status, code, key = 409, 127002, deliveryi18n.ProjectNameTaken
	case errors.Is(err, ErrParentNotClient):
		status, code, key = 422, 127003, deliveryi18n.ParentNotClient
	case errors.Is(err, ErrContactNotFound):
		status, code, key = 422, 127040, deliveryi18n.ContactNotFound
	case errors.Is(err, ErrIntakeNotFound):
		status, code, key = 422, 120404, deliveryi18n.IntakeNotFound
	case errors.Is(err, ErrInvalidMember), errors.Is(err, collab.ErrMemberInactive):
		status, code, key = 422, 127043, deliveryi18n.MemberRoleMismatch
	case errors.Is(err, ErrProjectLimit):
		status, code, key = 422, 127044, deliveryi18n.ProjectLimitReached
	case errors.Is(err, ErrProjectNonDelegable):
		status, code, key = 403, 120403, deliveryi18n.HumanOnly
		data = map[string]string{"reason_code": "NON_DELEGABLE"}
	case errors.Is(err, ErrProjectForbidden):
		status, code, key = 403, 120403, respx.Forbidden
		data = map[string]string{"reason_code": "NON_DELEGABLE"}
	case errors.Is(err, core.ErrAuthorizationChanged):
		status, code, key = 409, 120409, respx.InvalidArgument
	case errors.Is(err, ErrProjectNotFound), errors.Is(err, core.ErrEntityNotFound):
		status, code, key = 404, 120404, respx.NotFound
	case errors.Is(err, ErrInvalidArgument):
		status, code, key = 422, 120400, respx.InvalidArgument
	case errors.Is(err, ErrUnavailable), errors.Is(err, core.ErrUnavailable), errors.Is(err, collab.ErrUnavailable), errors.Is(err, events.ErrUnavailable), errors.Is(err, governance.ErrUnavailable), errors.Is(err, idempotency.ErrUnavailable):
		status, code, key = 503, 127004, deliveryi18n.ProvisioningUnavailable
	}
	respx.ReportCause(ctx, status, err)
	return respx.ErrStatus(ctx, status, code, key, data)
}
