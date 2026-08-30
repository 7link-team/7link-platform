package audit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func httpModuleConfig() Config {
	return Config{
		Enabled:         true,
		RecordReads:     false,
		RecordStreams:   true,
		MaxBodyBytes:    256,
		QueueSize:       1024,
		BatchSize:       1,
		FlushInterval:   time.Millisecond,
		FlushTimeout:    time.Second,
		SensitiveFields: nil,
	}
}

func runHTTP(t *testing.T, module *Module, request *http.Request, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	module.HTTPMiddleware(handler).ServeHTTP(recorder, request)
	return recorder
}

func TestHTTPMiddlewareSkipsReadsByDefault(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	runHTTP(t, module, httptest.NewRequest(http.MethodGet, "/orders", nil), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	assert.Empty(t, sink.snapshot())
}

func TestHTTPMiddlewareRecordsReadsWhenConfigured(t *testing.T) {
	config := httpModuleConfig()
	config.RecordReads = true
	module, sink := newTestModule(t, config)
	runHTTP(t, module, httptest.NewRequest(http.MethodGet, "/orders/42", nil), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, AccessRead, event.Access)
	assert.Equal(t, OutcomeSuccess, event.Outcome)
	assert.Equal(t, "GET /orders/42", event.Operation)
}

func TestHTTPMiddlewareRecordsWriteWithRedactedBody(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	handler := func(w http.ResponseWriter, r *http.Request) {
		// The handler must still see the full body.
		body := make([]byte, 1024)
		n, _ := r.Body.Read(body)
		require.Contains(t, string(body[:n]), "hunter2")
		Annotate(r.Context(), Action("order.create"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"id":"42"}}`))
	}
	request := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"name":"a","password":"hunter2"}`))
	request.Header.Set("Content-Type", "application/json")
	runHTTP(t, module, request, handler)

	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, AccessWrite, event.Access)
	assert.Equal(t, "POST /orders", event.Operation)
	assert.Equal(t, "order.create", event.Action)
	assert.NotContains(t, event.Request.Body, "hunter2")
	assert.Contains(t, event.Request.Body, "[REDACTED]")
	assert.Equal(t, int64(33), event.Request.Size)
	// Success response: summary only, resource ID extracted from the envelope.
	assert.Equal(t, "42", event.ResourceID)
	assert.Equal(t, "ok", event.Message)
	assert.Equal(t, OmittedSuccessSummary, event.Response.Omitted)
	assert.Empty(t, event.Response.Body)
}

func TestHTTPMiddlewareAnnotatePromotesRead(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	handler := func(w http.ResponseWriter, r *http.Request) {
		Annotate(r.Context(), SetAccess(AccessWrite))
		w.WriteHeader(http.StatusOK)
	}
	runHTTP(t, module, httptest.NewRequest(http.MethodGet, "/orders/42/export", nil), handler)
	eventually(t, func() bool { return sink.count() == 1 })
	assert.Equal(t, AccessWrite, sink.snapshot()[0].Access)
}

func TestHTTPMiddlewareSkipsConfiguredRoutes(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	runHTTP(t, module, httptest.NewRequest(http.MethodPost, "/healthz", nil), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	runHTTP(t, module, httptest.NewRequest(http.MethodPost, "/metrics", nil), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	runHTTP(t, module, httptest.NewRequest(http.MethodPost, "/openapi.json", nil), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	runHTTP(t, module, httptest.NewRequest(http.MethodPost, "/", nil), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	assert.Empty(t, sink.snapshot())
}

func TestHTTPMiddlewareCustomSkipRoutes(t *testing.T) {
	config := httpModuleConfig()
	config.SkipRoutes = []string{"/internal"}
	module, sink := newTestModule(t, config)
	runHTTP(t, module, httptest.NewRequest(http.MethodPost, "/internal/task", nil), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	assert.Empty(t, sink.snapshot())
}

func TestHTTPMiddlewareDoesNotCaptureNonJSONBody(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	request := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("raw-file-content"))
	request.Header.Set("Content-Type", "multipart/form-data")
	runHTTP(t, module, request, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, OmittedUnsupportedContent, event.Request.Omitted)
	assert.Empty(t, event.Request.Body)
}

func TestHTTPMiddlewareCapsOversizedJSONBody(t *testing.T) {
	config := httpModuleConfig()
	config.MaxBodyBytes = 8
	module, sink := newTestModule(t, config)
	request := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"name":"very-long-value"}`))
	request.Header.Set("Content-Type", "application/json")
	runHTTP(t, module, request, func(w http.ResponseWriter, r *http.Request) {
		// Handler still receives the whole body despite the audit cap.
		all, _ := readAllBody(r)
		assert.Equal(t, `{"name":"very-long-value"}`, all)
		w.WriteHeader(http.StatusCreated)
	})
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, OmittedTooLarge, event.Request.Omitted)
	assert.True(t, event.Request.Truncated)
	assert.Empty(t, event.Request.Body)
}

func TestHTTPMiddlewareDoesNotLeakOversizedSensitiveJSON(t *testing.T) {
	config := httpModuleConfig()
	config.MaxBodyBytes = 24
	module, sink := newTestModule(t, config)
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"password":"top-secret","padding":"large"}`))
	request.Header.Set("Content-Type", "application/json")
	runHTTP(t, module, request, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, OmittedTooLarge, event.Request.Omitted)
	assert.Empty(t, event.Request.Body)
	assert.NotContains(t, event.Request.Body, "top-secret")
}

func TestHTTPMiddlewareOmitsMalformedJSON(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"password":"top-secret"`))
	request.Header.Set("Content-Type", "application/json")
	runHTTP(t, module, request, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) })
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, OmittedInvalidJSON, event.Request.Omitted)
	assert.Empty(t, event.Request.Body)
}

func TestHTTPMiddlewareRecordsFailureResponseBody(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	request := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"name":"x"}`))
	request.Header.Set("Content-Type", "application/json")
	runHTTP(t, module, request, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":422,"message":"bad name","data":null}`))
	})
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, OutcomeFailure, event.Outcome)
	assert.Equal(t, http.StatusUnprocessableEntity, event.StatusCode)
	assert.Contains(t, event.Response.Body, "bad name")
}

func TestHTTPMiddlewareOmitsOversizedFailureResponse(t *testing.T) {
	config := httpModuleConfig()
	config.MaxBodyBytes = 16
	module, sink := newTestModule(t, config)
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	runHTTP(t, module, request, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"token":"top-secret","message":"denied"}`))
	})
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, OmittedTooLarge, event.Response.Omitted)
	assert.True(t, event.Response.Truncated)
	assert.Empty(t, event.Response.Body)
}

func TestHTTPMiddlewareSeesRecoveredPanic(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	// The recovery middleware must sit inside the audit middleware — exactly as
	// in the application chain — so the recovered 500 is still audited.
	recoverer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recover() != nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"code":500,"message":"internal","data":null}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
	request := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"name":"x"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	wrapped := module.HTTPMiddleware(recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))
	wrapped.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, OutcomeFailure, event.Outcome)
	assert.Equal(t, http.StatusInternalServerError, event.StatusCode)
	assert.Contains(t, event.Response.Body, "internal")
}

func TestHTTPMiddlewareRecordsStreamSession(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	websocket := httptest.NewRequest(http.MethodGet, "/ws", nil)
	websocket.Header.Set("Connection", "Upgrade")
	websocket.Header.Set("Upgrade", "websocket")
	runHTTP(t, module, websocket, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, KindStream, event.Entry)
	assert.Equal(t, http.StatusSwitchingProtocols, event.StatusCode)
	assert.Equal(t, OutcomeSuccess, event.Outcome)
	assert.Equal(t, OmittedStream, event.Response.Omitted)

	// SSE is also a stream session.
	sse := httptest.NewRequest(http.MethodGet, "/events", nil)
	sse.Header.Set("Accept", "text/event-stream")
	runHTTP(t, module, sse, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)
		flusher.Flush()
	})
	eventually(t, func() bool { return sink.count() == 2 })
	events := sink.snapshot()
	assert.Equal(t, KindStream, events[1].Entry)
}

func TestHTTPMiddlewareRecordsRejectedWebSocketHandshake(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	request := httptest.NewRequest(http.MethodGet, "/ws", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	runHTTP(t, module, request, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, KindStream, event.Entry)
	assert.Equal(t, http.StatusForbidden, event.StatusCode)
	assert.Equal(t, OutcomeFailure, event.Outcome)
}

func TestHTTPMiddlewareDetectsSSEFromResponse(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	request := httptest.NewRequest(http.MethodGet, "/events", nil)
	runHTTP(t, module, request, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
	})
	eventually(t, func() bool { return sink.count() == 1 })
	assert.Equal(t, KindStream, sink.snapshot()[0].Entry)
}

func TestHTTPMiddlewareSkipsStreamsWhenConfigured(t *testing.T) {
	config := httpModuleConfig()
	config.RecordStreams = false
	module, sink := newTestModule(t, config)
	sse := httptest.NewRequest(http.MethodGet, "/events", nil)
	sse.Header.Set("Accept", "text/event-stream")
	runHTTP(t, module, sse, func(w http.ResponseWriter, _ *http.Request) {})
	assert.Empty(t, sink.snapshot())
}

func TestHTTPMiddlewareDisabledPassesThrough(t *testing.T) {
	config := httpModuleConfig()
	config.Enabled = false
	module, sink := newTestModule(t, config)
	request := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"name":"x"}`))
	request.Header.Set("Content-Type", "application/json")
	runHTTP(t, module, request, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	assert.Empty(t, sink.snapshot())
}

func TestHTTPMiddlewareResponseRecorderPreservesStatus(t *testing.T) {
	module, _ := newTestModule(t, httpModuleConfig())
	// Multiple WriteHeader calls keep the first status.
	request := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	rec := runHTTP(t, module, request, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("ok"))
	})
	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestProbeEnvelope(t *testing.T) {
	code, message, id := probeEnvelope([]byte(`{"code":7,"message":"hi","data":{"id":"123","x":1}}`))
	assert.Equal(t, 7, code)
	assert.Equal(t, "hi", message)
	assert.Equal(t, "123", id)

	// Non-envelope body yields zeros.
	code, message, id = probeEnvelope([]byte(`not json`))
	assert.Zero(t, code)
	assert.Empty(t, message)
	assert.Empty(t, id)

	// List envelope without a single id.
	code, message, id = probeEnvelope([]byte(`{"code":0,"message":"ok","data":[]}`))
	assert.Zero(t, code)
	assert.Empty(t, id)
}

func TestCaptureRequestBodyRestoresStream(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("0123456789"))
	raw, size, limited := captureRequestBody(request, 4)
	assert.Nil(t, raw)
	assert.Equal(t, int64(10), size)
	assert.True(t, limited)
	all, err := readAllBody(request)
	require.NoError(t, err)
	assert.Equal(t, "0123456789", all)

	// Small bodies are not marked limited.
	request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("ab"))
	_, size, limited = captureRequestBody(request, 4)
	assert.Equal(t, int64(2), size)
	assert.False(t, limited)
	all, _ = readAllBody(request)
	assert.Equal(t, "ab", all)

	// Empty body returns nothing and leaves the request usable.
	request = httptest.NewRequest(http.MethodPost, "/x", nil)
	raw, size, limited = captureRequestBody(request, 4)
	assert.Nil(t, raw)
	assert.Zero(t, size)
	assert.False(t, limited)
}

func TestHTTPMiddlewareEmptyBodyWriteHasNoOmission(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	// An empty write body has nothing to capture, so it is not an unsupported
	// content type.
	request := httptest.NewRequest(http.MethodPost, "/x", nil)
	runHTTP(t, module, request, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	eventually(t, func() bool { return sink.count() == 1 })
	assert.Equal(t, OmittedNone, sink.snapshot()[0].Request.Omitted)
}

func TestHTTPMiddlewareNonJSONWriteBodyRecordedAsUnsupported(t *testing.T) {
	module, sink := newTestModule(t, httpModuleConfig())
	// A write with a body but a non-JSON content type is not captured.
	request := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("hello"))
	request.Header.Set("Content-Type", "text/plain")
	runHTTP(t, module, request, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	eventually(t, func() bool { return sink.count() == 1 })
	assert.Equal(t, OmittedUnsupportedContent, sink.snapshot()[0].Request.Omitted)
}

func readAllBody(request *http.Request) (string, error) {
	raw, err := io.ReadAll(request.Body)
	return string(raw), err
}

func TestResponseRecorderUnwrapAndFlush(t *testing.T) {
	base := httptest.NewRecorder()
	wrapped, recorder := newResponseRecorder(base, 16)
	_, _ = wrapped.Write([]byte("hello"))
	flusher, ok := wrapped.(http.Flusher)
	require.True(t, ok)
	flusher.Flush()
	assert.Equal(t, base, recorder.Unwrap())
	assert.Equal(t, "hello", string(recorder.body()))
}

func TestSensitiveFieldsConfigApplies(t *testing.T) {
	config := httpModuleConfig()
	config.SensitiveFields = []string{"phoneNumber"}
	module, sink := newTestModule(t, config)
	request := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"phoneNumber":"555-1234","name":"a"}`))
	request.Header.Set("Content-Type", "application/json")
	runHTTP(t, module, request, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	eventually(t, func() bool { return sink.count() == 1 })
	body := sink.snapshot()[0].Request.Body
	assert.NotContains(t, body, "555-1234")
	assert.Contains(t, body, "[REDACTED]")
}

func TestResponseRecorderHijackErrorPath(t *testing.T) {
	// The wrapper must not advertise an optional interface unsupported by the
	// underlying writer.
	wrapped, _ := newResponseRecorder(httptest.NewRecorder(), 16)
	_, ok := wrapped.(http.Hijacker)
	assert.False(t, ok)
}

func TestResponseRecorderTruncatesOversizedWrites(t *testing.T) {
	base := httptest.NewRecorder()
	wrapped, recorder := newResponseRecorder(base, 4)
	_, _ = wrapped.Write([]byte("0123456789"))
	assert.Equal(t, "0123", string(recorder.body()))
	assert.True(t, recorder.truncated)
	assert.Equal(t, int64(10), recorder.size)
	// A second oversized write keeps the flag set and the buffer capped.
	_, _ = wrapped.Write([]byte("more"))
	assert.Equal(t, "0123", string(recorder.body()))
}

func TestCaptureRequestBodyChunked(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("abcdef"))
	request.ContentLength = -1
	raw, size, limited := captureRequestBody(request, 3)
	assert.Nil(t, raw)
	assert.Equal(t, int64(4), size)
	assert.True(t, limited)
	// Restored stream is intact.
	all, _ := readAllBody(request)
	assert.Equal(t, "abcdef", all)
}

func TestCaptureRequestBodyChunkedOverCapIsLimited(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("abcd"))
	request.ContentLength = -1
	raw, size, limited := captureRequestBody(request, 3)
	assert.Nil(t, raw)
	assert.Equal(t, int64(4), size)
	assert.True(t, limited)
	all, _ := readAllBody(request)
	assert.Equal(t, "abcd", all)
}

func TestCaptureRequestBodyChunkedExactlyAtCapIsCaptured(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("abc"))
	request.ContentLength = -1
	raw, size, limited := captureRequestBody(request, 3)
	assert.Equal(t, []byte("abc"), raw)
	assert.Equal(t, int64(3), size)
	assert.False(t, limited)
}
