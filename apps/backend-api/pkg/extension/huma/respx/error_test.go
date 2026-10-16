package respx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"

	"7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsStatusErrorRecognizesAChosenResponseAnywhereInTheChain(t *testing.T) {
	chosen := huma.Error422UnprocessableEntity(InvalidID)
	assert.True(t, IsStatusError(chosen))
	assert.True(t, IsStatusError(fmt.Errorf("handler: %w", chosen)))
	assert.True(t, IsStatusError(huma.ErrorWithHeaders(chosen, http.Header{"Retry-After": {"1"}})))
	assert.True(t, IsStatusError(Err(t.Context(), 100001, InvalidArgument)))
	assert.False(t, IsStatusError(errors.New("domain failure")))
	assert.False(t, IsStatusError(nil))
}

func TestErrStatusCarriesItsStatusBusinessCodeAndData(t *testing.T) {
	detail := map[string]int{"revision": 7}
	err := ErrStatus(t.Context(), http.StatusConflict, 102003, "files.file_operation_conflict", detail)

	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, http.StatusConflict, status.GetStatus(), "the transport status is the caller's")
	raw, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)
	var body struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]int `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Equal(t, 102003, body.Code, "the business code, not the status, is the discriminator")
	assert.Equal(t, "files.file_operation_conflict", body.Message)
	assert.Equal(t, detail, body.Data)

	var plain *errorEnvelope
	require.ErrorAs(t, Err(t.Context(), 100001, InvalidArgument), &plain)
	assert.Nil(t, plain.Data, "Err keeps data null")
	assert.Equal(t, http.StatusBadRequest, plain.GetStatus())
}

// recordingHandler captures log records together with the context each was
// logged with, so a test can tell slog.ErrorContext from slog.Error.
type recordingHandler struct {
	records  []slog.Record
	contexts []context.Context
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(ctx context.Context, record slog.Record) error {
	h.records = append(h.records, record)
	h.contexts = append(h.contexts, ctx)
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) attr(index int, key string) string {
	value := ""
	h.records[index].Attrs(func(attr slog.Attr) bool {
		if attr.Key == key {
			value = attr.Value.String()
			return false
		}
		return true
	})
	return value
}

func recordLogs(t *testing.T) *recordingHandler {
	t.Helper()
	handler := &recordingHandler{}
	previous := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return handler
}

type traceKey struct{}

func TestCausesOfKeepsOnlyInternalErrors(t *testing.T) {
	cause := errors.New("database is down")
	got := causesOf([]error{nil, &huma.ErrorDetail{Message: "required"}, cause})
	assert.Equal(t, []error{cause}, got)
	assert.Empty(t, causesOf(nil))
}

func TestCausesOfCarriesTheCausesOfAResponseWrittenAgain(t *testing.T) {
	Install()
	cause := errors.New("replay store is down")
	mapped := huma.Error503ServiceUnavailable(ServiceUnavailable, cause)
	assert.Equal(t, []error{cause}, causesOf([]error{mapped}),
		"the root cause, not the message key, survives a re-write")
	assert.Empty(t, causesOf([]error{huma.Error404NotFound(NotFound)}),
		"a response without a cause contributes none")
}

func TestInstallRetainsTheCauseWithoutSerializingIt(t *testing.T) {
	Install()
	err := huma.Error500InternalServerError(InternalServerError, errors.New("dsn=postgres://secret"))
	var envelope *errorEnvelope
	require.ErrorAs(t, err, &envelope)
	require.Len(t, envelope.causes, 1)
	raw, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(raw), "secret")
	assert.NotContains(t, err.Error(), "secret")
}

func TestReportCauseLogsAServerFaultWithTheRequestContext(t *testing.T) {
	logs := recordLogs(t)
	ctx := context.WithValue(t.Context(), traceKey{}, "trace-1")

	ReportCause(ctx, http.StatusServiceUnavailable, errors.New("pool exhausted"), errors.New("retry failed"))

	require.Len(t, logs.records, 1)
	assert.Equal(t, slog.LevelError, logs.records[0].Level)
	assert.Equal(t, "trace-1", logs.contexts[0].Value(traceKey{}), "logged with the request context")
	assert.Contains(t, logs.attr(0, "error"), "pool exhausted")
	assert.Contains(t, logs.attr(0, "error"), "retry failed")
	assert.Equal(t, "503", logs.attr(0, "status"))
	assert.Empty(t, logs.attr(0, "operation"), "no operation is invented")
}

func TestReportCauseIgnoresClientFaultsAndMissingCauses(t *testing.T) {
	logs := recordLogs(t)
	ReportCause(t.Context(), http.StatusUnprocessableEntity, errors.New("bad input"))
	ReportCause(t.Context(), http.StatusInternalServerError)
	ReportCause(t.Context(), http.StatusInternalServerError, nil)
	assert.Empty(t, logs.records)
}

func TestReportCauseLogsAnAbandonedRequestAtDebug(t *testing.T) {
	logs := recordLogs(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	ReportCause(ctx, http.StatusInternalServerError, context.Canceled)
	require.Len(t, logs.records, 1)
	assert.Equal(t, slog.LevelDebug, logs.records[0].Level)
}

func TestReportCauseToleratesANilContext(t *testing.T) {
	logs := recordLogs(t)
	//nolint:staticcheck // a nil context is exactly what this guards against
	ReportCause(nil, http.StatusInternalServerError, errors.New("boom"))
	require.Len(t, logs.records, 1)
	assert.Equal(t, slog.LevelError, logs.records[0].Level)
}

func TestCachedErrorPreservesFirstEnvelopeAndExactNumbers(t *testing.T) {
	initI18n(t)
	ctx := i18n.WithLocale(t.Context(), "zh-CN")
	original := ErrStatus(ctx, http.StatusUnprocessableEntity, 127044, InvalidArgument, map[string]any{"amount": int64(9007199254740993)})
	body, err := MarshalEnvelope(ctx, original)
	require.NoError(t, err)
	restored, err := RestoreError(http.StatusUnprocessableEntity, body)
	require.NoError(t, err)
	replay, err := MarshalEnvelope(i18n.WithLocale(t.Context(), "en-US"), restored)
	require.NoError(t, err)
	require.JSONEq(t, string(body), string(replay))
	require.Contains(t, string(replay), "9007199254740993")
	require.Equal(t, http.StatusUnprocessableEntity, restored.GetStatus())
	for _, body := range []string{`null`, `{}`, `{"code":0,"message":"invalid"}`, `{"code":1,"message":""}`, `{"code":1,"message":"invalid"} {}`, `invalid`} {
		_, err := RestoreError(http.StatusBadRequest, []byte(body))
		require.Error(t, err)
	}
	_, err = RestoreError(http.StatusCreated, []byte(`{}`))
	require.Error(t, err)
	_, err = MarshalEnvelope(ctx, map[string]string{"message": "fake"})
	require.Error(t, err)
	_, err = MarshalEnvelope(ctx, OK(ctx, make(chan int)).Body)
	require.Error(t, err)
}
