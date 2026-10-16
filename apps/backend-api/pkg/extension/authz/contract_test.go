package authz_test

import (
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/i18n"
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
)

func TestRequestContractUsesSelectedAuthorizationAndSchema(t *testing.T) {
	_, api := humatest.New(t)
	checker := &fakeChecker{allowed: true}
	membership := &fakeMembership{active: true}
	verifier := validVerifier()
	registrar := authz.NewRegistrar(testRegistry(), verifier, checker, membership)
	calls := 0
	type input struct {
		Body struct {
			Name string  `json:"name" maxLength:"42"`
			Date *string `json:"date,omitempty" format:"date"`
		}
	}
	authz.Register(registrar, api, huma.Operation{OperationID: "contract-create", Method: http.MethodPost, Path: "/stores", Tags: []string{"tenants"}}, authz.Guard{Resource: "store", Verb: "view"}, func(context.Context, *input) (*routeOutput, error) {
		calls++
		return &routeOutput{Body: "created"}, nil
	})
	require.NoError(t, authz.RegisterRequestContract(api, "/tenants/{tenant_id}/stores"))
	require.Error(t, authz.RegisterRequestContract(api, "/tenants/{tenant_id}/stores"))
	require.Error(t, authz.RegisterRequestContract(api, "/missing"))
	require.NoError(t, authz.ValidateOperations(api, testRegistry()))
	accepted := api.Do(http.MethodOptions, "/tenants/11/stores?method=POST")
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	var result struct {
		Data authz.OperationContract `json:"data"`
	}
	require.NoError(t, json.Unmarshal(accepted.Body.Bytes(), &result))
	require.Equal(t, "contract-create", result.Data.OperationID)
	require.Equal(t, http.MethodPost, verifier.csrfCalls[0].method, "method-sensitive authorization evaluates the selected operation")
	require.Contains(t, accepted.Body.String(), `"maxLength":42`)
	require.NotContains(t, accepted.Body.String(), `"RouteOutput"`, "unrelated response schemas must not leak")
	again := api.Do(http.MethodOptions, "/tenants/11/stores?method=POST")
	var repeated struct {
		Data authz.OperationContract `json:"data"`
	}
	require.NoError(t, json.Unmarshal(again.Body.Bytes(), &repeated))
	require.Equal(t, result.Data, repeated.Data, "contract version and schemas are stable; request metadata is per request")
	checker.allowed = false
	denied := api.Do(http.MethodOptions, "/tenants/11/stores?method=POST")
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
	require.NotContains(t, denied.Body.String(), "maxLength")
	checker.allowed = true
	membership.active = false
	nonmember := api.Do(http.MethodOptions, "/tenants/11/stores?method=POST")
	require.Equal(t, http.StatusForbidden, nonmember.Code, nonmember.Body.String())
	membership.active = true
	preflight := api.Do(http.MethodOptions, "/tenants/11/stores?method=POST", "Access-Control-Request-Method: POST")
	require.Equal(t, http.StatusBadRequest, preflight.Code)
	unsupported := api.Do(http.MethodOptions, "/tenants/11/stores?method=DELETE")
	require.Equal(t, http.StatusUnprocessableEntity, unsupported.Code)
	require.Zero(t, calls, "contract reads never execute the business handler")
}

func TestRequestContractLocalizesMessagesWithoutChangingAuthorization(t *testing.T) {
	require.NoError(t, i18n.NewModule(nil, i18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	router := chi.NewMux()
	api := humax.New(router, humax.DefaultConfig("test", "1"))
	checker := &fakeChecker{allowed: true}
	registrar := authz.NewRegistrar(testRegistry(), validVerifier(), checker, &fakeMembership{active: true})
	calls := 0
	authz.Register(registrar, api, huma.Operation{OperationID: "localized-contract", Method: http.MethodPost, Path: "/stores", Tags: []string{"tenants"}}, authz.Guard{Resource: "store", Verb: "view"}, func(ctx context.Context, in *struct {
		Body struct {
			Name string `json:"name" maxLength:"42"`
		}
	}) (*routeOutput, error) {
		calls++
		return &routeOutput{Body: "created"}, nil
	})
	require.NoError(t, authz.RegisterRequestContract(api, "/tenants/{tenant_id}/stores"))
	request := func(locale string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodOptions, "/tenants/11/stores?method=POST", nil)
		req.Header.Set("Accept-Language", locale)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	versions := map[string]bool{}
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY", "en-US"} {
		response := request(locale)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Equal(t, locale, response.Header().Get("Content-Language"))
		require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
		var body respx.Envelope[authz.OperationContract]
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.Equal(t, locale, body.Data.Locale)
		expected, issues := respx.ValidationMessages(i18n.WithLocale(t.Context(), locale))
		require.Equal(t, expected, body.Data.Messages)
		require.Equal(t, issues, body.Data.IssueMessages)
		var again respx.Envelope[authz.OperationContract]
		require.NoError(t, json.Unmarshal(request(locale).Body.Bytes(), &again))
		require.Equal(t, body.Data, again.Data)
		versions[body.Data.Version] = true
	}
	require.Len(t, versions, 3, "language and localized messages participate in the version")
	checker.allowed = false
	denied := request("zh-CN")
	require.Equal(t, http.StatusForbidden, denied.Code)
	require.NotContains(t, denied.Body.String(), "message_key")
	require.NotContains(t, denied.Body.String(), "request_body")
	require.Zero(t, calls)
}
