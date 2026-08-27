package grpcui

import (
	"context"
	"fmt"
	"net/http"
	"time"

	grpcuix "7link.com/platform/pkg/extension/grpcui"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const Path = "/grpcui"

// Module owns the embedded grpcui HTTP routes and its connection to the local
// reflected gRPC server.
type Module struct {
	target      string
	dialOptions []grpc.DialOption
	conn        *grpc.ClientConn
}

func (*Module) ModuleName() string { return "grpcui" }

var (
	_ module.HTTPRegistrar = (*Module)(nil)
	_ module.Stopper       = (*Module)(nil)
)

func NewModule(target string, dialOptions ...grpc.DialOption) *Module {
	return &Module{target: target, dialOptions: append([]grpc.DialOption(nil), dialOptions...)}
}

func (m *Module) RegisterHTTP(ctx context.Context, router chi.Router, _ huma.API) error {
	dialOptions := append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, m.dialOptions...)
	conn, err := grpc.NewClient(m.target, dialOptions...)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	m.conn = conn

	discoveryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	handler, err := grpcuix.Handler(discoveryCtx, conn, m.target)
	if err != nil {
		_ = conn.Close()
		m.conn = nil
		return fmt.Errorf("discover reflected services: %w", err)
	}

	router.Handle(Path, http.RedirectHandler(Path+"/", http.StatusTemporaryRedirect))
	router.Handle(Path+"/*", http.StripPrefix(Path, handler))
	return nil
}

func (m *Module) Stop(context.Context) error {
	if m.conn == nil {
		return nil
	}
	err := m.conn.Close()
	m.conn = nil
	return err
}
