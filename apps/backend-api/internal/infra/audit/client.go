package audit

import (
	"context"
	"net"
	"net/http"
	"strings"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// clientFromRequest captures the client identity from an HTTP request. Only
// whitelisted headers are recorded; Authorization and Cookie are never touched.
// The peer address is the immediate socket peer — X-Forwarded-For is not
// trusted, because it is trivially spoofable.
func clientFromRequest(request *http.Request) Client {
	return Client{
		IP:        peerIP(request.RemoteAddr),
		UserAgent: request.Header.Get("User-Agent"),
		RequestID: firstNonEmpty(request.Header.Get("X-Request-ID"), request.Header.Get("X-Trace-ID")),
		Host:      request.Host,
	}
}

// clientFromGRPC captures the client identity from a gRPC call's context.
func clientFromGRPC(ctx context.Context) Client {
	client := Client{}
	if peerInfo, ok := peer.FromContext(ctx); ok && peerInfo.Addr != nil {
		client.IP = peerIP(peerInfo.Addr.String())
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		client.RequestID = firstNonEmpty(metadataValue(md, "x-request-id"), metadataValue(md, "x-trace-id"))
		client.UserAgent = metadataValue(md, "user-agent")
	}
	return client
}

func metadataValue(md metadata.MD, key string) string {
	values := md.Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// peerIP strips the port from a host:port address.
func peerIP(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}
