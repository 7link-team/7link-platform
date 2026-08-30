package audit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyHTTP(t *testing.T) {
	assert.Equal(t, AccessRead, classifyHTTP(http.MethodGet))
	assert.Equal(t, AccessRead, classifyHTTP(http.MethodHead))
	assert.Equal(t, AccessRead, classifyHTTP(http.MethodOptions))
	assert.Equal(t, AccessRead, classifyHTTP(http.MethodTrace))
	assert.Equal(t, AccessWrite, classifyHTTP(http.MethodPost))
	assert.Equal(t, AccessWrite, classifyHTTP(http.MethodPut))
	assert.Equal(t, AccessWrite, classifyHTTP(http.MethodPatch))
	assert.Equal(t, AccessWrite, classifyHTTP(http.MethodDelete))
	assert.Equal(t, AccessRead, classifyHTTP("get"))   // lower-case normalized
	assert.Equal(t, AccessWrite, classifyHTTP("post")) // lower-case normalized
}

func TestClassifyGRPC(t *testing.T) {
	assert.Equal(t, AccessRead, classifyGRPC("/order.v1.OrderService/GetOrder"))
	assert.Equal(t, AccessRead, classifyGRPC("/order.v1.OrderService/ListOrders"))
	assert.Equal(t, AccessRead, classifyGRPC("/order.v1.OrderService/SearchOrders"))
	assert.Equal(t, AccessRead, classifyGRPC("/order.v1.OrderService/CheckHealth"))
	assert.Equal(t, AccessRead, classifyGRPC("/order.v1.OrderService/WatchOrders"))
	assert.Equal(t, AccessWrite, classifyGRPC("/order.v1.OrderService/CreateOrder"))
	assert.Equal(t, AccessWrite, classifyGRPC("/order.v1.OrderService/UpdateOrder"))
	assert.Equal(t, AccessWrite, classifyGRPC("/order.v1.OrderService/DeleteOrder"))
	assert.Equal(t, AccessWrite, classifyGRPC("NoSlash"))
}

func TestIsSkipped(t *testing.T) {
	extra := []string{"/custom-skip"}
	for _, path := range []string{
		"/healthz", "/healthz/deep", "/livez", "/readyz", "/metrics", "/debug",
		"/debug/pprof", "/docs", "/docs/config.js", "/openapi", "/openapi.json", "/",
	} {
		assert.True(t, isSkipped(path, nil), path)
	}
	for _, path := range []string{
		"/orders", "/healthzish", "/metrix", "/custom-skip/thing",
	} {
		if path == "/custom-skip/thing" {
			assert.True(t, isSkipped(path, extra))
			continue
		}
		assert.False(t, isSkipped(path, nil), path)
	}
	assert.False(t, isSkipped("/orders", extra))
}

func TestIsStreamRequest(t *testing.T) {
	websocket := httptest.NewRequest(http.MethodGet, "/ws", nil)
	websocket.Header.Set("Connection", "Upgrade")
	websocket.Header.Set("Upgrade", "websocket")
	assert.True(t, isStreamRequest(websocket))

	sse := httptest.NewRequest(http.MethodGet, "/events", nil)
	sse.Header.Set("Accept", "text/event-stream")
	assert.True(t, isStreamRequest(sse))

	plain := httptest.NewRequest(http.MethodPost, "/orders", nil)
	assert.False(t, isStreamRequest(plain))
}
