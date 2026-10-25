package fleet

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type gatewayProxyHTTPInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           GatewayProxyInput
}
type gatewayProviderKeyInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Secret string `json:"secret" minLength:"4" maxLength:"4096" writeOnly:"true"`
	}
}

func (m *Module) registerGatewayProxyHTTP(api huma.API) error {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "model-gateway.provider-key.import", Method: http.MethodPost, Path: "/model-gateway/provider-keys", Summary: "Seal a provider credential without exposing it to runners", Tags: []string{"machines"}, DefaultStatus: 201, Errors: []int{401, 403, 409, 422, 503}}, authz.Guard{Resource: "gateway", Verb: "manage", FilterData: true}, m.importGatewayProviderKeyHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "model-gateway.response.proxy", Method: http.MethodPost, Path: "/model-gateway/responses", Summary: "Proxy a budgeted text Responses request to a configured provider", Tags: []string{"machines"}, MaxBodyBytes: 1024 * 1024, Errors: []int{401, 403, 409, 413, 422, 503}, Responses: map[string]*huma.Response{"200": {Description: "Upstream Responses JSON or SSE bytes; request metadata is recorded separately", Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Type: "object", AdditionalProperties: true}}, "text/event-stream": {Schema: &huma.Schema{Type: "string"}}}}}}, authz.Guard{Resource: "gateway", Verb: "manage", FilterData: true}, func(ctx context.Context, in *gatewayProxyHTTPInput) (*huma.StreamResponse, error) {
		return m.gatewayProxyHTTP(api, ctx, in)
	})
	for _, p := range []string{"/model-gateway/provider-keys", "/model-gateway/responses"} {
		if err := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+p); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) importGatewayProviderKeyHTTP(ctx context.Context, in *gatewayProviderKeyInput) (*respx.Body[GatewayKeyView], error) {
	if err := validateGatewaySecret(in.Body.Secret); err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return gatewayCommand(ctx, m, in.IdempotencyKey, "model-gateway.provider-key.import", in.Body, func(tx *gorm.DB) (GatewayKeyView, error) {
		return m.storeGatewayKeyOn(ctx, tx, authn.TenantIDFromContext(ctx), in.Body.Secret, true)
	})
}

func (m *Module) gatewayProxyHTTP(api huma.API, ctx context.Context, in *gatewayProxyHTTPInput) (*huma.StreamResponse, error) {
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	if m.idempotency == nil {
		return nil, fleetHTTPError(ctx, ErrUnavailable)
	}
	var route gatewayRoute
	created := false
	tenant := authn.TenantIDFromContext(ctx)
	hash, err := idempotency.HashRequest(http.MethodPost, "model-gateway.response.proxy", nil, in.Body)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.controlAuthorityOn(ctx, tx, tenant, "gateway_manage"); err != nil {
			return err
		}
		_, _, err := m.idempotency.Do(ctx, tx, idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "model-gateway.response.proxy", Value: in.IdempotencyKey, RequestHash: hash}, func(tx *gorm.DB) (idempotency.Result, error) {
			var err error
			route, err = m.prepareGatewayProxyOn(ctx, tx, tenant, in.Body)
			if err != nil {
				return idempotency.Result{}, err
			}
			created = true
			b, err := json.Marshal(gatewayRequestView(route.request))
			return idempotency.Result{StatusCode: 201, Body: b}, err
		})
		return err
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	if !created {
		return nil, fleetHTTPError(ctx, ErrVersionConflict)
	}
	return &huma.StreamResponse{Body: func(stream huma.Context) {
		requestCtx, cancel := context.WithTimeout(stream.Context(), 180*time.Second)
		defer cancel()
		watchDone := make(chan struct{})
		watchRoute := route
		watchRoute.key = ""
		go func() {
			defer close(watchDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-requestCtx.Done():
					return
				case <-ticker.C:
					if err := db.WithContext(requestCtx).Transaction(func(tx *gorm.DB) error { return m.gatewayProxyCurrentOn(requestCtx, tx, watchRoute) }); err != nil {
						cancel()
						return
					}
				}
			}
		}()
		defer func() { cancel(); <-watchDone }()
		var cost int64
		known, completed := false, false
		// Settlement survives browser cancellation. Its transaction includes the
		// terminal ledger event and never releases uncertain upstream usage.
		defer func() {
			finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer finishCancel()
			if err := db.WithContext(finishCtx).Transaction(func(tx *gorm.DB) error { return m.finishGatewayProxyOn(finishCtx, tx, route, cost, known, completed) }); err != nil {
				slog.ErrorContext(finishCtx, "gateway settlement remains pending", "request_id", route.request.ID.String())
			}
		}()
		response, err := gatewayUpstream(requestCtx, route, http.DefaultTransport)
		route.key = ""
		stream.SetHeader("Cache-Control", "no-store")
		stream.SetHeader("X-Content-Type-Options", "nosniff")
		stream.SetHeader("X-Gateway-Request-ID", "gwr_"+route.request.ID.String())
		if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
			if response != nil {
				_ = response.Body.Close()
			}
			_ = huma.WriteErr(api, stream, http.StatusServiceUnavailable, fleetHTTPError(ctx, ErrUnavailable).Error())
			return
		}
		defer func() { _ = response.Body.Close() }()
		expected := "application/json"
		if route.stream {
			expected = "text/event-stream"
		}
		if strings.Split(response.Header.Get("Content-Type"), ";")[0] != expected {
			_ = huma.WriteErr(api, stream, http.StatusServiceUnavailable, fleetHTTPError(ctx, ErrUnavailable).Error())
			return
		}
		stream.SetHeader("Content-Type", expected)
		stream.SetStatus(http.StatusOK)
		var flush func() error
		if writer, ok := stream.BodyWriter().(http.ResponseWriter); ok {
			control := http.NewResponseController(writer)
			_ = control.SetWriteDeadline(time.Now().Add(15 * time.Second))
			flush = func() error { _ = control.SetWriteDeadline(time.Now().Add(15 * time.Second)); return control.Flush() }
		}
		cost, known, completed, err = copyGatewayResponse(stream.BodyWriter(), response.Body, route.stream, route.price, flush)
		completed = err == nil && completed
	}}, nil
}
