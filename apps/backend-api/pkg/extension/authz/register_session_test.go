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
