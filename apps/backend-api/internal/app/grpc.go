package app

import (
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"

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

	app.grpcServer = grpc.NewServer(
		grpc.UnaryInterceptor(grpcx.LocaleUnaryInterceptor),
		grpc.StreamInterceptor(grpcx.LocaleStreamInterceptor),
	)
	app.registerGRPCModules(app.grpcServer)
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

func (app *App) grpcDialTarget() string {
	host := strings.TrimSpace(app.config.GrpcServer.Address)
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::", "[::]":
		host = "::1"
	}
	return net.JoinHostPort(host, strconv.Itoa(app.config.GrpcServer.Port))
}
