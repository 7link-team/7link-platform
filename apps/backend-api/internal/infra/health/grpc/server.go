package grpc

import (
	"context"

	healthi18n "7link.com/platform/internal/infra/health/i18n"
	healthv1 "7link.com/platform/internal/infra/health/proto/gen/go/health/v1"
	"7link.com/platform/internal/infra/health/service"
	"7link.com/platform/pkg/i18n"
	"google.golang.org/grpc"
)

// Server implements platform.health.v1.HealthService.
type Server struct {
	healthv1.UnimplementedHealthServiceServer
	service *service.Service
}

func NewServer(service *service.Service) *Server {
	return &Server{service: service}
}

func (s *Server) Register(registrar grpc.ServiceRegistrar) {
	healthv1.RegisterHealthServiceServer(registrar, s)
}

func (s *Server) Check(ctx context.Context, _ *healthv1.CheckRequest) (*healthv1.CheckResponse, error) {
	data := s.service.Check(ctx)
	modules := make([]*healthv1.ModuleStatus, 0, len(data.Modules))
	for _, module := range data.Modules {
		modules = append(modules, &healthv1.ModuleStatus{
			Name:   module.Name,
			Status: servingStatus(module.Status),
		})
	}
	return &healthv1.CheckResponse{
		Code:    0,
		Message: i18n.TContext(ctx, healthi18n.CheckCompleted),
		Data:    &healthv1.CheckData{Status: servingStatus(data.Status), Modules: modules},
	}, nil
}

func servingStatus(status service.Status) healthv1.CheckData_ServingStatus {
	switch status {
	case service.StatusServing:
		return healthv1.CheckData_SERVING_STATUS_SERVING
	case service.StatusNotServing:
		return healthv1.CheckData_SERVING_STATUS_NOT_SERVING
	default:
		return healthv1.CheckData_SERVING_STATUS_UNSPECIFIED
	}
}
