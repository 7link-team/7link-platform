package health

import (
	"context"

	healthgrpc "7link.com/platform/internal/infra/health/grpc"
	healthhttp "7link.com/platform/internal/infra/health/http"
	healthi18n "7link.com/platform/internal/infra/health/i18n"
	"7link.com/platform/internal/infra/health/service"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

type HealthModule struct {
	service *service.Service
	http    *healthhttp.Handler
	grpc    *healthgrpc.Server
}

var (
	_ module.Initializer   = (*HealthModule)(nil)
	_ module.Starter       = (*HealthModule)(nil)
	_ module.Stopper       = (*HealthModule)(nil)
	_ module.Deinitializer = (*HealthModule)(nil)
	_ module.HTTPRegistrar = (*HealthModule)(nil)
	_ module.GRPCRegistrar = (*HealthModule)(nil)
)

func NewModule(checkers ...service.Checker) *HealthModule {
	svc := service.New(checkers...)
	return &HealthModule{
		service: svc,
		http:    healthhttp.New(svc),
		grpc:    healthgrpc.NewServer(svc),
	}
}

// Service exposes the use cases for direct in-process calls.
func (h *HealthModule) Service() *service.Service {
	return h.service
}

func (h *HealthModule) Initialize(context.Context) error {
	return healthi18n.Register()
}

func (h *HealthModule) Start(context.Context) error {
	return nil
}

func (h *HealthModule) RegisterHTTP(_ context.Context, _ chi.Router, api huma.API) error {
	h.http.Register(api)
	return nil
}

func (h *HealthModule) RegisterGRPC(registrar grpc.ServiceRegistrar) {
	h.grpc.Register(registrar)
}

func (h *HealthModule) Stop(context.Context) error {
	return nil
}

func (h *HealthModule) Deinitialize(context.Context) error {
	return nil
}
