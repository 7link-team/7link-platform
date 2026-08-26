package http

import (
	"context"

	healthv1 "7link.com/platform/internal/infra/health/proto/gen/go/health/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
)

// RegisterGateway wires the health REST routes onto mux. It calls srv in-process
// rather than dialing the gRPC port, since both servers live in this process.
func RegisterGateway(ctx context.Context, mux *runtime.ServeMux, srv healthv1.HealthServiceServer) error {
	return healthv1.RegisterHealthServiceHandlerServer(ctx, mux, srv)
}
