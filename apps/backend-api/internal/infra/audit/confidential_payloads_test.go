package audit

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfidentialRequestNeverCapturedWithBodyOptIn(t *testing.T) {
	OmitConfidentialRequest(t.Context())
	for _, status := range []int{201, 403, 422, 500} {
		module, sink := newTestModule(t, httpModuleConfig())
		secret := `{"title":"PRIVATE-TITLE","steps":"PRIVATE-STEPS","expected":"PRIVATE-EXPECTED","is_hidden_acceptance":true}`
		request := httptest.NewRequest(http.MethodPost, "/entities/3/test-cases", strings.NewReader(secret))
		request.Header.Set("Content-Type", "application/json")
		var received []byte
		var readErr error
		runHTTP(t, module, request, func(w http.ResponseWriter, r *http.Request) {
			OmitConfidentialRequest(r.Context())
			received, readErr = io.ReadAll(r.Body)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"code":1,"message":"PRIVATE-ERROR","errors":[{"value":"PRIVATE-STEPS"}]}`))
		})
		require.NoError(t, readErr)
		require.Equal(t, secret, string(received))
		eventually(t, func() bool { return sink.count() == 1 })
		event := sink.snapshot()[0]
		all, err := json.Marshal(event)
		require.NoError(t, err)
		require.NotContains(t, string(all), "PRIVATE-")
		require.NotContains(t, event.Request.Body, "PRIVATE-")
		require.Contains(t, event.Request.Body, "confidential_resource")
	}
}
