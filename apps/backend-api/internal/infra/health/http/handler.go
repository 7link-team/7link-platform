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

func New(service *service.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "check-health",
		Method:      stdhttp.MethodGet,
		Path:        "/healthz",
		Summary:     "Check service health",
		Tags:        []string{"Health"},
	}, h.check)
}

func (h *Handler) check(ctx context.Context, _ *checkInput) (*respx.Body[service.CheckData], error) {
	return respx.OKMessage(ctx, h.service.Check(ctx), healthi18n.CheckSucceeded), nil
}
