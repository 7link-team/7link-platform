package otelx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	generatedUUID = "123e4567-e89b-42d3-a456-426614174000"
	generatedHex  = "123e4567e89b42d3a456426614174000"
)

func TestHTTPMiddlewareGeneratesAndReturnsTraceHeaders(t *testing.T) {
	module := NewModule("test", "1.0.0", WithIDGenerator(func() string { return generatedUUID }))
	t.Cleanup(func() { require.NoError(t, module.Deinitialize(context.Background())) })

	var contextTraceID string
	handler := module.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		contextTraceID = TraceID(request.Context())
		assert.Equal(t, generatedHex, request.Header.Get(TraceIDHeader))
		w.WriteHeader(http.StatusCreated)
	}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/widgets", nil))

	assert.Equal(t, generatedHex, contextTraceID)
	assert.Equal(t, generatedHex, response.Header().Get(TraceIDHeader))
	assert.True(t, strings.HasPrefix(response.Header().Get(TraceparentHeader), "00-"+generatedHex+"-"))
	assert.Contains(t, response.Header().Get("Access-Control-Expose-Headers"), TraceIDHeader)
}

func TestHTTPMiddlewareContinuesIncomingTrace(t *testing.T) {
	const (
		parentTraceID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		parentSpanID  = "bbbbbbbbbbbbbbbb"
	)
	var generated atomic.Int32
	module := NewModule("test", "1.0.0", WithIDGenerator(func() string {
		generated.Add(1)
		return generatedUUID
	}))
	t.Cleanup(func() { require.NoError(t, module.Deinitialize(context.Background())) })

	handler := module.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, parentTraceID, TraceID(request.Context()))
	}))
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set(TraceparentHeader, "00-"+parentTraceID+"-"+parentSpanID+"-01")
	request.Header.Set(TraceIDHeader, generatedHex)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, parentTraceID, response.Header().Get(TraceIDHeader))
	assert.Zero(t, generated.Load())
}

func TestHTTPMiddlewareAcceptsUUIDTraceHeader(t *testing.T) {
	module := NewModule("test", "1.0.0")
	t.Cleanup(func() { require.NoError(t, module.Deinitialize(context.Background())) })

	handler := module.HTTPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		assert.Equal(t, generatedHex, TraceID(request.Context()))
	}))
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set(TraceIDHeader, generatedUUID)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, generatedHex, response.Header().Get(TraceIDHeader))
}

func TestHTTPTransportPropagatesTraceHeaders(t *testing.T) {
	module := NewModule("test", "1.0.0", WithIDGenerator(func() string { return generatedUUID }))
	t.Cleanup(func() { require.NoError(t, module.Deinitialize(context.Background())) })

	seen := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		seen <- request.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	ctx, span := module.Tracer("test").Start(t.Context(), "parent")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	client := &http.Client{Transport: module.HTTPTransport(nil)}
	response, err := client.Do(request)
	span.End()
	require.NoError(t, err)
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)

	headers := <-seen
	assert.Equal(t, generatedHex, headers.Get(TraceIDHeader))
	assert.Contains(t, headers.Get(TraceparentHeader), generatedHex)
}
