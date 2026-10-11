package guid

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func httpCounter() func() (int64, error) {
	var value atomic.Int64
	return func() (int64, error) { return value.Add(1), nil }
}

// bearerAuthenticator stands in for the authorization layer: it admits a call
// that carries a bearer credential and records which operations it guarded.
type bearerAuthenticator struct{ guarded []string }

func (a *bearerAuthenticator) Authenticate(api huma.API, op *huma.Operation) {
	a.guarded = append(a.guarded, op.OperationID)
	op.Security = []map[string][]string{{"bearer": {}}}
	op.Middlewares = append(op.Middlewares, func(ctx huma.Context, next func(huma.Context)) {
		if ctx.Header("Authorization") == "" {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, respx.Unauthorized)
			return
		}
		next(ctx)
	})
}

const bearer = "Authorization: Bearer token"

func TestHTTPEndpoints(t *testing.T) {
	_, api := humatest.New(t)
	registerHTTP(api, httpCounter(), &bearerAuthenticator{})

	response := api.Get("/guid", bearer)
	require.Equal(t, http.StatusOK, response.Code)
	var one struct {
		Data string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &one))
	assert.Equal(t, "1", one.Data)

	response = api.Get("/guid/3", bearer)
	require.Equal(t, http.StatusOK, response.Code)
	var batch struct {
		Data []string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &batch))
	assert.Equal(t, []string{"2", "3", "4"}, batch.Data)

	response = api.Get("/guid/100", bearer)
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &batch))
	assert.Len(t, batch.Data, 100)

	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/guid/0", bearer).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/guid/101", bearer).Code, "a batch is capped at 100 (D107)")
}

// TestHTTPEndpointsRequireAnAuthenticatedCaller pins D107: both operations go
// through the authenticator, so an anonymous call never reaches the generator.
func TestHTTPEndpointsRequireAnAuthenticatedCaller(t *testing.T) {
	var calls atomic.Int64
	authenticator := &bearerAuthenticator{}
	_, api := humatest.New(t)
	registerHTTP(api, func() (int64, error) { return calls.Add(1), nil }, authenticator)

	assert.Equal(t, []string{"next-guid", "next-guid-batch"}, authenticator.guarded)
	assert.Equal(t, http.StatusUnauthorized, api.Get("/guid").Code)
	assert.Equal(t, http.StatusUnauthorized, api.Get("/guid/2").Code)
	assert.Zero(t, calls.Load(), "an anonymous call draws no ID")
	for _, path := range []string{"/guid", "/guid/{count}"} {
		assert.NotEmpty(t, api.OpenAPI().Paths[path].Get.Security, "%s declares its credential", path)
	}
}

// TestHTTPEndpointsWithoutAnAuthenticatorRefuseEveryCall fails closed: with no
// authorization layer to authenticate the caller, the API stays published but
// never generates for anyone.
func TestHTTPEndpointsWithoutAnAuthenticatorRefuseEveryCall(t *testing.T) {
	var calls atomic.Int64
	_, api := humatest.New(t)
	registerHTTP(api, func() (int64, error) { return calls.Add(1), nil }, nil)
	assert.Equal(t, http.StatusServiceUnavailable, api.Get("/guid", bearer).Code)
	assert.Equal(t, http.StatusServiceUnavailable, api.Get("/guid/2", bearer).Code)
	assert.Zero(t, calls.Load())
}

func TestHTTPEndpointsHideGeneratorFailures(t *testing.T) {
	_, api := humatest.New(t)
	registerHTTP(api, func() (int64, error) { return 0, assert.AnError }, &bearerAuthenticator{})
	assert.Equal(t, http.StatusServiceUnavailable, api.Get("/guid", bearer).Code)
	assert.Equal(t, http.StatusServiceUnavailable, api.Get("/guid/2", bearer).Code)
}

func TestRequestIDsParseThroughOneRule(t *testing.T) {
	for _, test := range []struct {
		value    string
		optional bool
		valid    bool
		want     ID
	}{
		{value: "12", valid: true, want: 12},
		{value: " 12", valid: true, want: 12},
		{value: "12 ", valid: true, want: 12},
		{value: `"12"`, valid: true, want: 12},
		{value: "9223372036854775807", valid: true, want: 9223372036854775807},
		{value: ""},
		{value: "   "},
		{value: "0"},
		{value: "-12"},
		{value: "null"},
		{value: "abc"},
		{value: "1.5"},
		{value: "12x"},
		{value: "9223372036854775808"},
		{value: "", optional: true, valid: true},
		{value: "  ", optional: true, valid: true},
		{value: "7", optional: true, valid: true, want: 7},
		{value: "0", optional: true},
		{value: "abc", optional: true},
	} {
		parse := RequireID
		if test.optional {
			parse = OptionalID
		}
		id, err := parse(test.value)
		if test.valid {
			require.NoError(t, err, "%q optional=%v", test.value, test.optional)
			assert.Equal(t, test.want, id, "%q", test.value)
			continue
		}
		var status huma.StatusError
		require.ErrorAs(t, err, &status, "%q optional=%v", test.value, test.optional)
		assert.Equal(t, http.StatusUnprocessableEntity, status.GetStatus(), "%q", test.value)
		assert.Equal(t, respx.InvalidID, status.Error(), "%q", test.value)
		assert.Zero(t, id, "%q", test.value)
	}
}
