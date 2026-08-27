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
	status := healthv1.CheckData_SERVING_STATUS_UNSPECIFIED
	if data.Status == service.StatusServing {
		status = healthv1.CheckData_SERVING_STATUS_SERVING
	} else if data.Status == service.StatusNotServing {
		status = healthv1.CheckData_SERVING_STATUS_NOT_SERVING
	}
	return &healthv1.CheckResponse{
		Code:    0,
		Message: i18n.TContext(ctx, healthi18n.CheckSucceeded),
		Data:    &healthv1.CheckData{Status: status},
	}, nil
}
