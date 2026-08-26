package app

import (
	"fmt"
	"log/slog"
	"net"

	healthgrpc "7link.com/platform/internal/infra/health/grpc"
	healthv1 "7link.com/platform/internal/infra/health/proto/gen/go/health/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func (app *App) startGRPCServer() error {
	addr := fmt.Sprintf("%s:%d", app.config.GrpcServer.Address, app.config.GrpcServer.Port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen %s: %w", addr, err)
	}

	app.grpcServer = grpc.NewServer()
	app.healthServer = healthgrpc.NewServer()
	healthv1.RegisterHealthServiceServer(app.grpcServer, app.healthServer)
	// Lets grpcurl/grpcui discover services without a local .proto copy.
	reflection.Register(app.grpcServer)

	go func() {
		slog.Info("grpc server listening", "addr", addr)
		if err := app.grpcServer.Serve(lis); err != nil {
			app.serveErr <- fmt.Errorf("grpc serve: %w", err)
		}
	}()
	return nil
}
