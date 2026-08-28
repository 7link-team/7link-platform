package health

import (
	"context"
	"errors"
	"sync/atomic"

	healthgrpc "7link.com/platform/internal/infra/health/grpc"
	healthhttp "7link.com/platform/internal/infra/health/http"
	healthi18n "7link.com/platform/internal/infra/health/i18n"
	"7link.com/platform/internal/infra/health/service"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

var ErrNotReady = errors.New("health: application is not ready")

const Name = healthi18n.Namespace

type HealthModule struct {
	service *service.Service
	http    *healthhttp.Handler
	grpc    *healthgrpc.Server
	ready   atomic.Bool
}

func (*HealthModule) ModuleName() string { return Name }

func (*HealthModule) ModuleDependencies() []string { return []string{platformi18n.Name} }

var (
	_ module.Initializer   = (*HealthModule)(nil)
	_ module.Depender      = (*HealthModule)(nil)
	_ module.Starter       = (*HealthModule)(nil)
	_ module.HealthChecker = (*HealthModule)(nil)
	_ module.Stopper       = (*HealthModule)(nil)
	_ module.HTTPRegistrar = (*HealthModule)(nil)
	_ module.GRPCRegistrar = (*HealthModule)(nil)
)

func NewModule(providers ...service.CheckerProvider) *HealthModule {
	svc := service.New(providers...)
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
	h.ready.Store(true)
	return nil
}

func (h *HealthModule) CheckHealth(context.Context) error {
	if !h.ready.Load() {
		return ErrNotReady
	}
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
	h.ready.Store(false)
	return nil
}
