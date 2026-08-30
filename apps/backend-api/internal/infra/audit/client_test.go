package audit

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

func TestClientFromRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/x", nil)
	request.RemoteAddr = "192.0.2.10:5678"
	request.Header.Set("User-Agent", "curl/8")
	request.Header.Set("X-Request-ID", "req-1")
	client := clientFromRequest(request)
	assert.Equal(t, "192.0.2.10", client.IP)
	assert.Equal(t, "curl/8", client.UserAgent)
	assert.Equal(t, "req-1", client.RequestID)
	assert.Equal(t, "example.com", client.Host) // httptest default Host
}

func TestClientFromGRPC(t *testing.T) {
	ctx := context.Background()
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(
		"x-request-id", "req-9",
		"x-trace-id", "ignored",
		"user-agent", "grpc-client",
	))
	ctx = peer.NewContext(ctx, &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("10.1.2.3"), Port: 4000}})
	client := clientFromGRPC(ctx)
	assert.Equal(t, "10.1.2.3", client.IP)
	assert.Equal(t, "req-9", client.RequestID) // first non-empty wins
	assert.Equal(t, "grpc-client", client.UserAgent)

	// Empty metadata yields an empty client.
	assert.Equal(t, Client{}, clientFromGRPC(context.Background()))
}

func TestMetadataValue(t *testing.T) {
	md := metadata.Pairs("k", "v1", "k", "v2")
	assert.Equal(t, "v1", metadataValue(md, "k"))
	assert.Equal(t, "", metadataValue(md, "missing"))
}

func TestFirstNonEmptyAndPeerIP(t *testing.T) {
	assert.Equal(t, "a", firstNonEmpty("", "a", "b"))
	assert.Equal(t, "", firstNonEmpty("", ""))
	assert.Equal(t, "1.2.3.4", peerIP("1.2.3.4:8080"))
	assert.Equal(t, "bare", peerIP("bare"))
}
