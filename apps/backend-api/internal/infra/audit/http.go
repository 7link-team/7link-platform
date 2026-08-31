package audit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// HTTPMiddleware audits every non-skipped HTTP request. Read operations are
// skipped unless record_reads is enabled. When capture_bodies is explicitly
// enabled, write operations capture their JSON request body (redacted and
// size-capped) and, on failure, a safe error-response body. WebSocket upgrades and
// Server-Sent Events are audited as sessions
// (established/closed) without per-message bodies.
//
// Mount it inside the telemetry middleware (so trace/span IDs are available)
// and outside the recovery middleware (so recovered panics still produce a 500
// audit event).
func (m *Module) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.enabled() {
			next.ServeHTTP(w, r)
			return
		}

		path := r.URL.Path
		if isSkipped(path, m.skipRoutes) {
			next.ServeHTTP(w, r)
			return
		}

		access := classifyHTTP(r.Method)
		requestStream := isStreamRequest(r)
		if requestStream && !m.recordStreams() {
			next.ServeHTTP(w, r)
			return
		}

		entryKind := KindHTTP
		if requestStream {
			entryKind = KindStream
		}
		entry := &inflight{
			event: &Event{
				OccurredAt: time.Now().UTC(),
				Entry:      entryKind,
				Operation:  r.Method + " " + path,
				Access:     access,
				Actor:      actorOf(r.Context(), Actor{}),
				Client:     clientFromRequest(r),
				Target:     Target{Method: r.Method, Route: path},
			},
		}
		ctx := withInflight(r.Context(), entry)
		r = r.WithContext(ctx)
		var requestCounter *countingReadCloser

		// Request body capture: only for writes, never for streams, only for
		// JSON content types. Bodies known to be oversized are left untouched;
		// chunked bodies are probed and restored so the handler sees every byte.
		if m.captureBodies() && !requestStream && access == AccessWrite && isJSONContentType(r.Header.Get("Content-Type")) {
			raw, size, limited := captureRequestBody(r, m.maxBodyBytes())
			entry.reqBody = raw
			entry.reqSize = size
			entry.reqLimited = limited
		} else if !m.captureBodies() && !requestStream && access == AccessWrite && r.Body != nil && r.Body != http.NoBody {
			requestCounter = &countingReadCloser{ReadCloser: r.Body}
			r.Body = requestCounter
		}

		// Request-declared streams do not capture response bodies. The wrapper
		// still tracks status and preserves only the optional interfaces exposed
		// by the underlying writer.
		responseLimit := m.maxBodyBytes()
		if requestStream || !m.captureBodies() {
			responseLimit = 0
		}
		wrapped, recorder := newResponseRecorder(w, responseLimit)
		w = wrapped

		started := time.Now()
		next.ServeHTTP(w, r)
		if requestCounter != nil {
			entry.reqSize = requestCounter.size
		}
		entry.mu.Lock()
		entry.event.DurationMS = float64(time.Since(started).Nanoseconds()) / 1e6
		entry.mu.Unlock()

		isStream := requestStream || recorder.hijacked || isSSEContentType(recorder.Header().Get("Content-Type"))
		m.finalizeHTTP(ctx, entry, recorder, r, isStream)
	})
}

// finalizeHTTP builds the persisted event from the in-flight entry, applying
// the record decision (writes always; reads only when configured), the outcome,
// and the redacted request/response payloads.
func (m *Module) finalizeHTTP(
	ctx context.Context,
	entry *inflight,
	recorder *responseRecorder,
	r *http.Request,
	isStream bool,
) {
	// Snapshot under the inflight lock so a concurrent Annotate cannot race with
	// the fields read and recorded below.
	event := entry.snapshot()

	// The in-flight access may have been promoted by business code via
	// Annotate(ctx, SetAccess(...)); re-read it for the record decision.
	if isStream && !m.recordStreams() {
		return
	}
	record := event.Access == AccessWrite || m.recordReads() || isStream
	if !record {
		return
	}

	if isStream {
		event.Entry = KindStream
		event.StatusCode = recorder.code
		if event.StatusCode >= 400 {
			event.Outcome = OutcomeFailure
		} else {
			event.Outcome = OutcomeSuccess
		}
		event.Response = Payload{Omitted: OmittedStream}
		m.record(ctx, &event)
		return
	}

	status := http.StatusOK
	if recorder != nil {
		status = recorder.code
	}
	event.StatusCode = status
	if status >= 400 {
		event.Outcome = OutcomeFailure
	} else {
		event.Outcome = OutcomeSuccess
	}

	if recorder.body() != nil {
		if status >= 400 {
			if recorder.truncated {
				event.Response = Payload{Size: recorder.size, Truncated: true, Omitted: OmittedTooLarge}
			} else if body, truncated, err := m.redactAndTruncate(recorder.body()); err != nil {
				event.Response = Payload{Size: recorder.size, Omitted: OmittedInvalidJSON}
			} else {
				event.Response = Payload{Body: body, Size: recorder.size, Truncated: truncated}
			}
		} else {
			code, message, resourceID := probeEnvelope(recorder.body())
			if code != 0 {
				event.Code = code
			}
			event.Message = message
			if event.ResourceID == "" && resourceID != "" {
				event.ResourceID = resourceID
			}
			event.Response = Payload{Size: recorder.size, Omitted: OmittedSuccessSummary}
		}
	} else if status >= 400 {
		omitted := OmittedNone
		if !m.captureBodies() && recorder.size > 0 {
			omitted = OmittedCaptureDisabled
		}
		event.Response = Payload{Size: recorder.size, Omitted: omitted}
	} else {
		event.Response = Payload{Size: recorder.size, Omitted: OmittedSuccessSummary}
	}

	if !m.captureBodies() && event.Access == AccessWrite {
		omitted := OmittedNone
		if r.ContentLength != 0 {
			omitted = OmittedCaptureDisabled
		}
		size := max(r.ContentLength, int64(0))
		if r.ContentLength < 0 {
			size = entry.reqSize
		}
		event.Request = Payload{Size: size, Omitted: omitted}
	} else if entry.reqLimited {
		event.Request = Payload{Size: entry.reqSize, Truncated: true, Omitted: OmittedTooLarge}
	} else if entry.reqBody != nil {
		if body, truncated, err := m.redactAndTruncate(entry.reqBody); err != nil {
			event.Request = Payload{Size: entry.reqSize, Omitted: OmittedInvalidJSON}
		} else {
			event.Request = Payload{Body: body, Size: entry.reqSize, Truncated: truncated}
		}
	} else if event.Access == AccessWrite {
		// A write with no captured body: either the body was empty (nothing to
		// capture) or the content type was not JSON and the body was deliberately
		// not read. ContentLength is -1 for chunked bodies; clamp it so the
		// recorded size is never negative.
		omitted := OmittedUnsupportedContent
		if r.ContentLength == 0 {
			omitted = OmittedNone
		}
		event.Request = Payload{Size: max(r.ContentLength, int64(0)), Omitted: omitted}
	}

	m.record(ctx, &event)
}

// captureRequestBody captures a complete body only when it fits within max and
// restores all bytes for the handler. Known oversized bodies are not read at
// all; chunked bodies are probed up to max+1 and omitted when oversized.
func captureRequestBody(r *http.Request, max int) (raw []byte, size int64, limited bool) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, 0, false
	}
	contentLength := r.ContentLength
	if max > 0 && contentLength > int64(max) {
		return nil, contentLength, true
	}
	original := r.Body
	if max <= 0 {
		raw, _ = io.ReadAll(original)
		r.Body = &restoredRequestBody{Reader: bytes.NewReader(raw), Closer: original}
		if contentLength >= 0 {
			return raw, contentLength, false
		}
		return raw, int64(len(raw)), false
	}
	reader := io.LimitReader(r.Body, int64(max)+1)
	raw, _ = io.ReadAll(reader)

	// Restore the bytes consumed by the probe followed by the unread remainder.
	parts := make([]io.Reader, 0, 2)
	if len(raw) > 0 {
		parts = append(parts, bytes.NewReader(raw))
	}
	parts = append(parts, original)
	r.Body = &restoredRequestBody{Reader: io.MultiReader(parts...), Closer: original}

	if len(raw) == 0 {
		return nil, 0, false
	}
	size = int64(len(raw))
	limited = len(raw) > max
	if limited {
		return nil, size, true
	}
	return raw, size, false
}

type restoredRequestBody struct {
	io.Reader
	io.Closer
}

type countingReadCloser struct {
	io.ReadCloser
	size int64
}

func (body *countingReadCloser) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	body.size += int64(count)
	return count, err
}

// responseRecorder captures the status, size, and (capped) body of a response
// while forwarding every write to the underlying writer.
type responseRecorder struct {
	http.ResponseWriter

	buf         bytes.Buffer
	max         int
	code        int
	size        int64
	truncated   bool
	wroteHeader bool
	hijacked    bool
}

func newResponseRecorder(w http.ResponseWriter, max int) (http.ResponseWriter, *responseRecorder) {
	recorder := &responseRecorder{ResponseWriter: w, max: max, code: http.StatusOK}
	flusher, canFlush := w.(http.Flusher)
	hijacker, canHijack := w.(http.Hijacker)
	switch {
	case canFlush && canHijack:
		return &flushHijackResponseRecorder{responseRecorder: recorder, flusher: flusher, hijacker: hijacker}, recorder
	case canFlush:
		return &flushResponseRecorder{responseRecorder: recorder, flusher: flusher}, recorder
	case canHijack:
		return &hijackResponseRecorder{responseRecorder: recorder, hijacker: hijacker}, recorder
	default:
		return recorder, recorder
	}
}

// body returns the captured response bytes (capped at the configured limit).
func (recorder *responseRecorder) body() []byte {
	return recorder.buf.Bytes()
}

func (recorder *responseRecorder) WriteHeader(status int) {
	if recorder.wroteHeader {
		return
	}
	// 1xx informational responses (e.g. 103 Early Hints) are not the final
	// status: forward them and keep waiting for the real one.
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		recorder.ResponseWriter.WriteHeader(status)
		return
	}
	recorder.wroteHeader = true
	recorder.code = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Write(body []byte) (int, error) {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	if recorder.max > 0 {
		if recorder.buf.Len() < recorder.max {
			remaining := recorder.max - recorder.buf.Len()
			if len(body) > remaining {
				recorder.buf.Write(body[:remaining])
				recorder.truncated = true
			} else {
				recorder.buf.Write(body)
			}
		} else if len(body) > 0 {
			recorder.truncated = true
		}
	}
	recorder.size += int64(len(body))
	return recorder.ResponseWriter.Write(body)
}

// Unwrap lets http.ResponseController reach the underlying writer's optional
// interfaces.
func (recorder *responseRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}

type flushResponseRecorder struct {
	*responseRecorder
	flusher http.Flusher
}

func (recorder *flushResponseRecorder) Flush() {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	recorder.flusher.Flush()
}

type hijackResponseRecorder struct {
	*responseRecorder
	hijacker http.Hijacker
}

func (recorder *hijackResponseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, readWriter, err := recorder.hijacker.Hijack()
	if err == nil {
		recorder.hijacked = true
		recorder.code = http.StatusSwitchingProtocols
		recorder.wroteHeader = true
	}
	return conn, readWriter, err
}

type flushHijackResponseRecorder struct {
	*responseRecorder
	flusher  http.Flusher
	hijacker http.Hijacker
}

func (recorder *flushHijackResponseRecorder) Flush() {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	recorder.flusher.Flush()
}

func (recorder *flushHijackResponseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, readWriter, err := recorder.hijacker.Hijack()
	if err == nil {
		recorder.hijacked = true
		recorder.code = http.StatusSwitchingProtocols
		recorder.wroteHeader = true
	}
	return conn, readWriter, err
}

// probeEnvelope extracts the {code,message,data.id} identity from a response
// body in the platform envelope shape. Non-envelope bodies yield zero values.
func probeEnvelope(body []byte) (code int, message, resourceID string) {
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return 0, "", ""
	}
	var data struct {
		ID any `json:"id"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err == nil && data.ID != nil {
		resourceID = fmt.Sprintf("%v", data.ID)
	}
	return envelope.Code, envelope.Message, resourceID
}
