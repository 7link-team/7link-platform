package app

import (
	"fmt"
	"log/slog"
	"net"
	"strconv"

	grpcx "7link.com/platform/pkg/extension/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func (app *App) startGRPCServer() error {
	addr := net.JoinHostPort(app.config.GrpcServer.Address, strconv.Itoa(app.config.GrpcServer.Port))
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen %s: %w", addr, err)
	}

	serverOptions := []grpc.ServerOption{}
	if app.telemetry != nil {
		serverOptions = append(serverOptions,
			grpc.StatsHandler(app.telemetry.GRPCServerStatsHandler()),
			grpc.ChainUnaryInterceptor(app.telemetry.UnaryServerInterceptor, grpcx.LocaleUnaryInterceptor),
			grpc.ChainStreamInterceptor(app.telemetry.StreamServerInterceptor, grpcx.LocaleStreamInterceptor),
		)
	} else {
		serverOptions = append(serverOptions,
			grpc.UnaryInterceptor(grpcx.LocaleUnaryInterceptor),
			grpc.StreamInterceptor(grpcx.LocaleStreamInterceptor),
		)
	}
	app.grpcServer = grpc.NewServer(serverOptions...)
	app.registerGRPCModules(app.grpcServer)
	// Lets grpcurl discover services without a local .proto copy.
	reflection.Register(app.grpcServer)

	go func() {
		slog.Info("grpc server listening", "addr", addr)
		if err := app.grpcServer.Serve(lis); err != nil {
			app.serveErr <- fmt.Errorf("grpc serve: %w", err)
		}
	}()
	return nil
}
