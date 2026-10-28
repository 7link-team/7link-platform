package authn

import (
	"context"
	"testing"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type registrationPolicyVerifier struct{ claims *authnext.Claims }

func (v registrationPolicyVerifier) Authenticate(context.Context, string, string) (*authnext.Claims, error) {
	if v.claims == nil {
		return nil, authnext.ErrInvalidToken
	}
	return v.claims, nil
}

type registrationPolicyChecker struct {
	allowed    bool
	permission string
}

func (*registrationPolicyChecker) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return false, nil
}
func (c *registrationPolicyChecker) CheckPlatform(_ context.Context, permission string, _ guid.ID) (bool, error) {
	c.permission = permission
	return c.allowed, nil
}
func (*registrationPolicyChecker) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

func TestRegistrationPolicyHTTPRequiresSystemAuthority(t *testing.T) {
	s, _ := newDirectRegistrationService(t)
	for _, test := range []struct {
		name    string
		claims  *authnext.Claims
		allowed bool
		status  int
	}{
		{name: "anonymous", status: 401},
		{name: "tenant operator", claims: &authnext.Claims{PrincipalID: 12, SubjectType: authnext.SubjectTypePrincipal}, status: 403},
		{name: "system administrator", claims: &authnext.Claims{PrincipalID: 12, SubjectType: authnext.SubjectTypePrincipal}, allowed: true, status: 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			checker := &registrationPolicyChecker{allowed: test.allowed}
			registry := authz.MustRegistry(authz.Action{Resource: "platform", Verb: "administer", Scope: authz.ActionScopeSystem})
			registrar := authz.NewRegistrar(registry, registrationPolicyVerifier{claims: test.claims}, checker, checker)
			_, api := humatest.New(t)
			registerRegistrationPolicyHTTP(api, s, registrar)
			get := api.Get("/system/settings/registration", "Authorization: Bearer test")
			require.Equal(t, test.status, get.Code, get.Body.String())
			put := api.Put("/system/settings/registration", "Authorization: Bearer test", map[string]any{"enabled": false})
			require.Equal(t, test.status, put.Code, put.Body.String())
			if test.claims != nil {
				assert.Equal(t, "platform_administer", checker.permission)
			}
			if test.allowed {
				assert.Contains(t, put.Body.String(), `"enabled":false`)
				assert.Contains(t, put.Body.String(), `"available":true`)
				assert.False(t, s.Capabilities().Registration)
			}
		})
	}
}
