package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
)

type systemAuditVerifier struct{ view *authnext.AccountViewIdentity }

func (v systemAuditVerifier) Authenticate(context.Context, string, string) (*authnext.Claims, error) {
	return &authnext.Claims{Subject: "9", PrincipalID: 9, SubjectType: authnext.SubjectTypePrincipal, AccountView: v.view}, nil
}

type systemAuditChecker struct{ grants map[string]bool }

func (*systemAuditChecker) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return false, nil
}

func (c *systemAuditChecker) CheckPlatform(_ context.Context, permission string, subject guid.ID) (bool, error) {
	return subject == 9 && c.grants[permission], nil
}

func TestSystemAuditRoutesEnforceExactPartitionAndPermissions(t *testing.T) {
	for _, mode := range []string{"normal", "read_only", "read_write"} {
		t.Run(mode, func(t *testing.T) {
			h, _ := newAuditHTTPHarness(t)
			global, err := h.service.Append(t.Context(), EventInput{TenantID: 0, EventType: "system_visible", Outcome: "success"})
			require.NoError(t, err)
			foreign, err := h.service.Append(t.Context(), EventInput{TenantID: 11, EventType: "tenant_secret", Outcome: "success"})
			require.NoError(t, err)
			checker := &systemAuditChecker{grants: map[string]bool{}}
			verifier := systemAuditVerifier{}
			if mode != "normal" {
				verifier.view = &authnext.AccountViewIdentity{ID: 50, PrincipalID: 7, ScopeType: "system", ScopeID: 1, ReadOnly: mode == "read_only"}
			}
			_, api := humatest.New(t)
			RegisterHTTP(api, h.service, authz.NewRegistrar(authz.DefaultRegistry(), verifier, checker, auditMembershipChecker{}))
			routes := []string{"/system/audit/events", "/system/audit/events/" + global.ID.String(), "/system/audit/integrity-verifications"}
			for _, route := range append(routes, "/system/audit/exports") {
				require.Equal(t, http.StatusForbidden, api.Get(route, "Authorization: Bearer test").Code)
			}
			checker.grants["system_audit_event_view"] = true
			for _, route := range routes {
				response := api.Get(route, "Authorization: Bearer test")
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.NotContains(t, response.Body.String(), "tenant_secret")
				require.Equal(t, http.StatusUnprocessableEntity, api.Get(route+"?tenant_id=11", "Authorization: Bearer test").Code)
			}
			require.Equal(t, http.StatusNotFound, api.Get("/system/audit/events/"+foreign.ID.String(), "Authorization: Bearer test").Code)
			require.Equal(t, http.StatusForbidden, api.Get("/system/audit/exports", "Authorization: Bearer test").Code, "view permission must not authorize export")
			checker.grants["system_audit_event_export"] = true
			response := api.Get("/system/audit/exports", "Authorization: Bearer test")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "system_visible")
			require.NotContains(t, response.Body.String(), "tenant_secret")
			require.Equal(t, "application/x-ndjson", response.Header().Get("Content-Type"))
			require.Equal(t, http.StatusUnprocessableEntity, api.Get("/system/audit/exports?tenant_id=11", "Authorization: Bearer test").Code)
			events, _, err := h.service.List(t.Context(), Filter{TenantID: 0, EventType: "audit_export_requested", Limit: 50})
			require.NoError(t, err)
			require.Len(t, events, 1)
			if mode == "normal" {
				require.Equal(t, guid.ID(9), events[0].PrincipalID)
			} else {
				require.Equal(t, guid.ID(7), events[0].PrincipalID, "the operator is the export actor")
				var detail map[string]any
				require.NoError(t, json.Unmarshal(events[0].Detail, &detail))
				require.Equal(t, "9", detail["viewed_principal_id"])
				require.Equal(t, "50", detail["account_view_id"])
				require.Equal(t, mode == "read_only", detail["account_view_read_only"])
			}
			checker.grants = map[string]bool{"system_audit_event_export": true}
			require.Equal(t, http.StatusForbidden, api.Get(routes[0], "Authorization: Bearer test").Code, "export permission must not authorize listing")
			for _, path := range routes {
				op := api.OpenAPI().Paths[path]
				if op == nil {
					continue
				}
				for _, parameter := range op.Get.Parameters {
					require.NotEqual(t, "tenant_id", parameter.Name, fmt.Sprintf("system schema %s", path))
				}
			}
		})
	}
}

func TestSystemAuditRejectsNonSystemAccountView(t *testing.T) {
	h, _ := newAuditHTTPHarness(t)
	_, api := humatest.New(t)
	verifier := systemAuditVerifier{view: &authnext.AccountViewIdentity{ID: 50, PrincipalID: 7, ScopeType: "tenant", ScopeID: 11}}
	checker := &systemAuditChecker{grants: map[string]bool{"system_audit_event_view": true, "system_audit_event_export": true}}
	RegisterHTTP(api, h.service, authz.NewRegistrar(authz.DefaultRegistry(), verifier, checker, auditMembershipChecker{}))
	for _, path := range []string{"/system/audit/events", "/system/audit/exports", "/system/audit/integrity-verifications"} {
		require.Equal(t, http.StatusForbidden, api.Get(path, "Authorization: Bearer test").Code)
	}
}
