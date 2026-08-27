// Package grpcuix embeds grpcui in the application's HTTP server.
package grpcuix

import (
	"context"
	"net/http"

	"github.com/fullstorydev/grpcui/standalone"
	"google.golang.org/grpc"
)

// Handler discovers all services through gRPC server reflection and returns a
// self-contained web UI handler, including its static assets and RPC endpoint.
func Handler(ctx context.Context, conn grpc.ClientConnInterface, target string) (http.Handler, error) {
	return standalone.HandlerViaReflection(ctx, conn, target)
}
