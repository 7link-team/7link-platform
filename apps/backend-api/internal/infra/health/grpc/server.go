package grpc

import (
	"context"

	healthv1 "7link.com/platform/internal/infra/health/proto/gen/go/health/v1"
)

// Server implements platform.health.v1.HealthService.
type Server struct {
	healthv1.UnimplementedHealthServiceServer
}

func NewServer() *Server {
	return &Server{}
}

// Check reports liveness. ponytail: always SERVING — if the process can answer,
// it is alive. Aggregate per-dependency readiness here when there are deps.
func (s *Server) Check(ctx context.Context, req *healthv1.CheckRequest) (*healthv1.CheckResponse, error) {
	return &healthv1.CheckResponse{Status: healthv1.CheckResponse_SERVING}, nil
}
