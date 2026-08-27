package http

import (
	"context"
	stdhttp "net/http"

	healthi18n "7link.com/platform/internal/infra/health/i18n"
	"7link.com/platform/internal/infra/health/service"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type Handler struct {
	service *service.Service
}

type checkInput struct{}

type checkOutput struct {
	Status int
	Body   respx.Envelope[service.CheckData]
}

func New(service *service.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "check-liveness",
		Method:      stdhttp.MethodGet,
		Path:        "/livez",
		Summary:     "Check service liveness",
		Tags:        []string{"Health"},
	}, h.liveness)
	huma.Register(api, huma.Operation{
		OperationID: "check-health",
		Method:      stdhttp.MethodGet,
		Path:        "/healthz",
		Summary:     "Check service liveness and module health",
		Tags:        []string{"Health"},
	}, h.check)
	huma.Register(api, huma.Operation{
		OperationID: "check-readiness",
		Method:      stdhttp.MethodGet,
		Path:        "/readyz",
		Summary:     "Check service readiness",
		Tags:        []string{"Health"},
	}, h.readiness)
	readyOperation := api.OpenAPI().Paths["/readyz"].Get
	if success := readyOperation.Responses["200"]; success != nil {
		unavailable := *success
		unavailable.Description = "Service is not ready"
		readyOperation.Responses["503"] = &unavailable
	}
}

func (h *Handler) liveness(ctx context.Context, _ *checkInput) (*checkOutput, error) {
	return output(ctx, stdhttp.StatusOK, h.service.Liveness(), healthi18n.CheckCompleted), nil
}

func (h *Handler) check(ctx context.Context, _ *checkInput) (*checkOutput, error) {
	return output(ctx, stdhttp.StatusOK, h.service.Check(ctx), healthi18n.CheckCompleted), nil
}

func (h *Handler) readiness(ctx context.Context, _ *checkInput) (*checkOutput, error) {
	data := h.service.Check(ctx)
	if data.Status == service.StatusServing {
		return output(ctx, stdhttp.StatusOK, data, healthi18n.ReadinessSucceeded), nil
	}
	return output(ctx, stdhttp.StatusServiceUnavailable, data, healthi18n.ReadinessFailed), nil
}

func output(ctx context.Context, status int, data service.CheckData, message string) *checkOutput {
	body := respx.OKMessage(ctx, data, message)
	if status >= stdhttp.StatusBadRequest {
		body.Body.Code = status
	}
	return &checkOutput{Status: status, Body: body.Body}
}
