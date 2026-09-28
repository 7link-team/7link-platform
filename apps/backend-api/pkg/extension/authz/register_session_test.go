package authz_test

import (
	"context"
	"net/http"
	"testing"

	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
)

func TestSessionRegistrationRejectsEvenOtherwiseValidBearerAndDocumentsCookie(t *testing.T) {
	_, api := humatest.New(t)
	verifier := &fakeVerifier{claims: &authn.Claims{PrincipalID: 1}, cookie: "session"}
	r := authz.NewAuthenticationOnlyRegistrar(verifier)
	op := huma.Operation{OperationID: "personal-security", Method: http.MethodGet, Path: "/personal-security"}
	authz.RegisterSessionAuthenticated(r, api, op, func(context.Context, *struct{}) (*routeOutput, error) { return &routeOutput{Body: "ok"}, nil })
	assert.Equal(t, []map[string][]string{{authz.SessionScheme: {}}}, api.OpenAPI().Paths[op.Path].Get.Security)
	response := api.Get(op.Path, "Cookie: session=valid")
	assert.Equal(t, http.StatusOK, response.Code)
	for _, authorization := range []string{"Bearer valid-token", "Basic credentials", "arbitrary"} {
		response = api.Get(op.Path, "Cookie: session=valid", "Authorization: "+authorization)
		assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	}
}

// A principal session mount is where self-service handlers stop checking who
// the caller is, so every subject other than a signed-in principal is refused
// before they run, as is a principal addressing another principal's path.
func TestPrincipalSessionRegistrationAdmitsOnlyThePrincipalItAddresses(t *testing.T) {
	for _, test := range []struct {
		name   string
		claims *authn.Claims
		path   string
		cookie string
		want   int
	}{
		{name: "no session cookie", claims: &authn.Claims{PrincipalID: 7, SubjectType: authn.SubjectTypePrincipal}, path: "/principals/7/closure", want: http.StatusUnauthorized},
		{name: "principal", claims: &authn.Claims{PrincipalID: 7, SubjectType: authn.SubjectTypePrincipal}, path: "/principals/7/closure", cookie: "session=valid", want: http.StatusOK},
		{name: "service account", claims: &authn.Claims{PrincipalID: 7, SubjectType: authn.SubjectTypeServiceAccount}, path: "/principals/7/closure", cookie: "session=valid", want: http.StatusUnauthorized},
		{name: "untyped subject", claims: &authn.Claims{PrincipalID: 7}, path: "/principals/7/closure", cookie: "session=valid", want: http.StatusUnauthorized},
		{name: "another principal", claims: &authn.Claims{PrincipalID: 7, SubjectType: authn.SubjectTypePrincipal}, path: "/principals/8/closure", cookie: "session=valid", want: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, api := humatest.New(t)
			r := authz.NewAuthenticationOnlyRegistrar(&fakeVerifier{claims: test.claims, cookie: "session"})
			var seen int64
			authz.RegisterPrincipalSessionAuthenticated(r, api, huma.Operation{
				OperationID: "closure", Method: http.MethodGet, Path: "/principals/{principal_id}/closure",
			}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
				seen = authn.PrincipalIDFromContext(ctx).Int64()
				return &routeOutput{Body: "ok"}, nil
			})
			var headers []any
			if test.cookie != "" {
				headers = append(headers, "Cookie: "+test.cookie)
			}
			response := api.Get(test.path, headers...)
			assert.Equal(t, test.want, response.Code, response.Body.String())
			if test.want == http.StatusOK {
				assert.Equal(t, int64(7), seen)
			} else {
				assert.Zero(t, seen, "the handler never runs for a refused caller")
			}
		})
	}
}
