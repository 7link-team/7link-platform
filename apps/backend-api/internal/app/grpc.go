package app

import (
	"fmt"
	"log/slog"
	"net"
	"strconv"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

func (app *App) startGRPCServer() error {
	addr := net.JoinHostPort(app.config.GrpcServer.Address, strconv.Itoa(app.config.GrpcServer.Port))
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen %s: %w", addr, err)
	}

	serverOptions := grpcServerOptions(app.config.GrpcServer)
	if app.modules != nil {
		serverOptions = append(serverOptions, app.modules.GRPCProtocolOptions()...)
	}
	app.grpcServer = grpc.NewServer(serverOptions...)
	if app.modules != nil {
		if err := app.modules.RegisterGRPC(app.grpcServer); err != nil {
			return err
		}
	}
	if app.config.GrpcServer.Reflection {
		// Lets grpcurl discover services without a local .proto copy.
		reflection.Register(app.grpcServer)
	}

	go func() {
		slog.Info("grpc server listening", "addr", addr)
		if err := app.grpcServer.Serve(lis); err != nil {
			app.serveErr <- fmt.Errorf("grpc serve: %w", err)
		}
	}()
	return nil
}

func grpcServerOptions(config GrpcServer) []grpc.ServerOption {
	options := make([]grpc.ServerOption, 0, 5)
	if config.MaxRecvMessageBytes > 0 {
		options = append(options, grpc.MaxRecvMsgSize(config.MaxRecvMessageBytes))
	}
	if config.MaxSendMessageBytes > 0 {
		options = append(options, grpc.MaxSendMsgSize(config.MaxSendMessageBytes))
	}
	parameters := keepalive.ServerParameters{
		MaxConnectionIdle:     config.MaxConnectionIdle,
		MaxConnectionAge:      config.MaxConnectionAge,
		MaxConnectionAgeGrace: config.MaxConnectionAgeGrace,
		Time:                  config.KeepaliveTime,
		Timeout:               config.KeepaliveTimeout,
	}
	if parameters != (keepalive.ServerParameters{}) {
		options = append(options, grpc.KeepaliveParams(parameters))
	}
	policy := keepalive.EnforcementPolicy{
		MinTime:             config.KeepaliveMinTime,
		PermitWithoutStream: config.PermitWithoutStream,
	}
	if policy != (keepalive.EnforcementPolicy{}) {
		options = append(options, grpc.KeepaliveEnforcementPolicy(policy))
	}
	return options
}
