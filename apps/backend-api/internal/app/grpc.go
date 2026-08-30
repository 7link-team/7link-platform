package app

import (
	"fmt"
	"log/slog"
	"net"
	"strconv"

	grpcx "7link.com/platform/pkg/extension/grpc"
	"7link.com/platform/pkg/module"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/stats"
)

func (app *App) startGRPCServer() error {
	addr := net.JoinHostPort(app.config.GrpcServer.Address, strconv.Itoa(app.config.GrpcServer.Port))
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen %s: %w", addr, err)
	}

	serverOptions := grpcServerOptions(app.config.GrpcServer)
	serverOptions = append(serverOptions, app.grpcProtocolOptions()...)
	app.grpcServer = grpc.NewServer(serverOptions...)
	app.registerGRPCModules(app.grpcServer)
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

// grpcProtocolOptions assembles the server's protocol options from the module
// registry. Protocol modules (telemetry, audit, ...) contribute their stats
// handlers and interceptors through the module interfaces in registration order
// (earlier module = outer interceptor); the platform interceptors (locale,
// recovery, validation) always run innermost.
func (app *App) grpcProtocolOptions() []grpc.ServerOption {
	var stats []stats.Handler
	var unary []grpc.UnaryServerInterceptor
	var stream []grpc.StreamServerInterceptor
	for _, appModule := range app.mods {
		if statsModule, ok := appModule.(module.StatsHandlerModule); ok {
			stats = append(stats, statsModule.GRPCServerStatsHandler())
		}
		if interceptor, ok := appModule.(module.UnaryInterceptorModule); ok {
			unary = append(unary, interceptor.UnaryServerInterceptor)
		}
		if interceptor, ok := appModule.(module.StreamInterceptorModule); ok {
			stream = append(stream, interceptor.StreamServerInterceptor)
		}
	}
	unary = append(unary,
		grpcx.LocaleUnaryInterceptor,
		grpcx.RecoveryUnaryInterceptor,
		grpcx.ValidationUnaryInterceptor,
	)
	stream = append(stream,
		grpcx.LocaleStreamInterceptor,
		grpcx.RecoveryStreamInterceptor,
		grpcx.ValidationStreamInterceptor,
	)

	options := make([]grpc.ServerOption, 0, len(stats)+2)
	for _, handler := range stats {
		options = append(options, grpc.StatsHandler(handler))
	}
	options = append(options,
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
	)
	return options
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
