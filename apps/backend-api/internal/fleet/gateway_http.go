package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	fleeti18n "7link.com/platform/internal/fleet/i18n"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type gatewayWriteInput struct {
	IfMatch string `header:"If-Match" required:"true"`
	Body    GatewayWrite
}
type gatewayOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[GatewayView]
}
type gatewayRequestInput struct {
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           struct {
		Model           string  `json:"model" minLength:"1" maxLength:"160"`
		ProjectID       guid.ID `json:"project_id"`
		MaxInputTokens  int64   `json:"max_input_tokens" minimum:"1" maximum:"1000000"`
		MaxOutputTokens int64   `json:"max_output_tokens" minimum:"1" maximum:"1000000"`
	}
}
type gatewayKeyInput struct {
	IdempotencyKey string `header:"Idempotency-Key"`
}

type gatewayKeyRotationInput struct {
	KeyID          string `path:"key_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Version int    `json:"version" minimum:"1"`
		Reason  string `json:"reason" minLength:"10" maxLength:"2000"`
	}
}

func (m *Module) rotateGatewayKeyHTTP(ctx context.Context, in *gatewayKeyRotationInput) (*respx.Body[GatewayKeyView], error) {
	id, err := guid.RequireID(in.KeyID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	return gatewayCommand(ctx, m, in.IdempotencyKey, "model-gateway.key.rotate", in, func(tx *gorm.DB) (GatewayKeyView, error) {
		return m.RotateGatewayKeyOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.Version, in.Body.Reason)
	})
}

// Shared platform receipts contain only these secret-free public DTOs.
func gatewayCommand[T any](ctx context.Context, m *Module, key, operation string, body any, run func(*gorm.DB) (T, error)) (*respx.Body[T], error) {
	db, e := m.writer()
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	var out T
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.controlAuthorityOn(ctx, tx, authn.TenantIDFromContext(ctx), "gateway_manage"); err != nil {
			return err
		}
		if key == "" {
			var err error
			out, err = run(tx)
			return err
		}
		if m.idempotency == nil {
			return ErrUnavailable
		}
		hash, err := idempotency.HashRequest(http.MethodPost, operation, nil, body)
		if err != nil {
			return err
		}
		result, _, err := m.idempotency.Do(ctx, tx, idempotency.Key{TenantID: authn.TenantIDFromContext(ctx), SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: key, RequestHash: hash}, func(tx *gorm.DB) (idempotency.Result, error) {
			v, e := run(tx)
			if e != nil {
				return idempotency.Result{}, e
			}
			b, e := json.Marshal(v)
			return idempotency.Result{StatusCode: 201, Body: b}, e
		})
		if err != nil {
			return err
		}
		return json.Unmarshal(result.Body, &out)
	})
	if e != nil {
		if errors.Is(e, idempotency.ErrInvalidKey) || errors.Is(e, idempotency.ErrKeyReused) {
			e = ErrInvalidArgument
		}
		if errors.Is(e, idempotency.ErrInProgress) {
			e = ErrVersionConflict
		}
		return nil, fleetHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) registerGatewayHTTP(api huma.API) error {
	if err := m.registerGatewayProxyHTTP(api); err != nil {
		return err
	}
	authz.Register(m.registrar, api, huma.Operation{OperationID: "model-gateway.key.rotate", Method: http.MethodPost, Path: "/model-gateway/keys/{key_id}/rotations", Summary: "Rotate a sealed gateway key and revoke the old reference atomically", Tags: []string{"machines"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 409, 422, 503}}, authz.Guard{Resource: "gateway", Verb: "manage", FilterData: true}, m.rotateGatewayKeyHTTP)
	if err := authz.RegisterRequestContract(api, "/tenants/{tenant_id}/model-gateway/keys/{key_id}/rotations"); err != nil {
		return err
	}
	authz.Register(m.registrar, api, huma.Operation{OperationID: "model-gateway.config.get", Method: http.MethodGet, Path: "/model-gateway/config", Summary: "Read persisted gateway configuration", Tags: []string{"machines"}, Errors: []int{401, 403, 503}}, authz.Guard{Resource: "gateway", Verb: "manage", FilterData: true}, m.getGatewayHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "model-gateway.config.update", Method: http.MethodPut, Path: "/model-gateway/config", Summary: "Update gateway providers models prices and project limits", Tags: []string{"machines"}, SkipValidateParams: true, Errors: []int{401, 403, 404, 409, 422, 428, 503}}, authz.Guard{Resource: "gateway", Verb: "manage", FilterData: true}, m.saveGatewayHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "model-gateway.key.create", Method: http.MethodPost, Path: "/model-gateway/keys", Summary: "Generate a sealed gateway credential reference", Tags: []string{"machines"}, DefaultStatus: 201, Errors: []int{401, 403, 422, 503}}, authz.Guard{Resource: "gateway", Verb: "manage", FilterData: true}, m.createGatewayKeyHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "model-gateway.request.list", Method: http.MethodGet, Path: "/model-gateway/requests", Summary: "Read latest one hundred gateway request metadata records", Tags: []string{"machines"}, Errors: []int{401, 403, 503}}, authz.Guard{Resource: "gateway", Verb: "manage", FilterData: true}, m.gatewayRequestsHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "model-gateway.request.create", Method: http.MethodPost, Path: "/model-gateway/requests", Summary: "Record a gateway request and its actual unavailable outcome", Tags: []string{"machines"}, DefaultStatus: 201, Errors: []int{401, 403, 409, 422, 503}}, authz.Guard{Resource: "gateway", Verb: "manage", FilterData: true}, m.queueGatewayHTTP)
	for _, p := range []string{"/model-gateway/config", "/model-gateway/keys", "/model-gateway/requests"} {
		if e := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+p); e != nil {
			return e
		}
	}
	return nil
}
func (m *Module) getGatewayHTTP(ctx context.Context, _ *struct{}) (*gatewayOutput, error) {
	db, e := m.writer()
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	var out GatewayView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.GatewayOn(ctx, tx, authn.TenantIDFromContext(ctx))
		return err
	})
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	return &gatewayOutput{ETag: strconv.Quote(strconv.Itoa(out.Version)), Body: respx.OK(ctx, out).Body}, nil
}
func (m *Module) saveGatewayHTTP(ctx context.Context, in *gatewayWriteInput) (*gatewayOutput, error) {
	if in.IfMatch == "" {
		return nil, respx.ErrStatus(ctx, 428, 140428, fleeti18n.Invalid, nil)
	}
	v, e := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if e != nil || v < 0 {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	db, e := m.writer()
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	var out GatewayView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.SaveGatewayOn(ctx, tx, authn.TenantIDFromContext(ctx), v, in.Body)
		return err
	})
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	return &gatewayOutput{ETag: strconv.Quote(strconv.Itoa(out.Version)), Body: respx.OK(ctx, out).Body}, nil
}
func (m *Module) createGatewayKeyHTTP(ctx context.Context, in *gatewayKeyInput) (*respx.Body[GatewayKeyView], error) {
	return gatewayCommand(ctx, m, in.IdempotencyKey, "gateway.key.create", nil, func(tx *gorm.DB) (GatewayKeyView, error) {
		return m.CreateGatewayKeyOn(ctx, tx, authn.TenantIDFromContext(ctx))
	})
}
func (m *Module) queueGatewayHTTP(ctx context.Context, in *gatewayRequestInput) (*respx.Body[GatewayRequestView], error) {
	return gatewayCommand(ctx, m, in.IdempotencyKey, "gateway.request.create", in.Body, func(tx *gorm.DB) (GatewayRequestView, error) {
		return m.QueueProjectGatewayRequestOn(ctx, tx, authn.TenantIDFromContext(ctx), in.Body.ProjectID, in.Body.Model, in.Body.MaxInputTokens, in.Body.MaxOutputTokens)
	})
}
func (m *Module) gatewayRequestsHTTP(ctx context.Context, _ *struct{}) (*respx.Body[[]GatewayRequestView], error) {
	db, e := m.writer()
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	out := []GatewayRequestView{}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if err := m.controlAuthorityOn(ctx, tx, tenant, "gateway_manage"); err != nil {
			return err
		}
		q := query.BindGatewayRequests(tx, m.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant)).Order(q.ID.Desc()).Limit(100).Find()
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, gatewayRequestView(r))
		}
		return nil
	})
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
