package composition_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"7link.com/platform/internal/execution"
	businessgovernance "7link.com/platform/internal/governance"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVerifierRegistryComposedCurrentIAMAndExternalDenials(t *testing.T) {
	a := bootstrapComposedApp(t)
	db := a.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, tenant := range []int{201, 202} {
		require.NoError(t, db.Table("iam_tenants").Create(map[string]any{"id": tenant, "slug": fmt.Sprintf("registry-%d", tenant), "name": "Verifier test", "status": "active", "version": 1, "created_at": now, "updated_at": now}).Error)
	}
	for _, id := range []int{301, 302, 303} {
		require.NoError(t, db.Table("iam_principals").Create(map[string]any{"id": id, "login_name": fmt.Sprintf("verifier-%d", id), "display_name": "Registry actor", "status": "active", "created_at": now, "updated_at": now}).Error)
	}
	bootstrap, err := module.Resolve[core.TenantBootstrapper](a.Modules().Services())
	require.NoError(t, err)
	for tenant, owner := range map[guid.ID]guid.ID{201: 301, 202: 303} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: tenant, OwnerID: owner, DisplayName: "Owner"}, now)
			return err
		}))
	}
	require.NoError(t, db.Table("iam_tenant_members").Create(map[string]any{"tenant_id": 201, "principal_id": 302, "status": "active", "created_at": now, "updated_at": now}).Error)
	require.NoError(t, db.Table("intake_contacts").Create(map[string]any{"id": 401, "tenant_id": 201, "kind": "PERSON", "display_name": "External", "principal_id": 302, "state": "ACTIVE", "version": 1, "created_at": now, "updated_at": now}).Error)
	roles, err := module.Resolve[businessgovernance.SeededRoles](a.Modules().Services())
	require.NoError(t, err)
	owner := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 201, PrincipalID: 301, SubjectType: authn.SubjectTypePrincipal})
	require.NoError(t, roles.EnsureTenant(owner, 201))
	web, err := module.Resolve[iamauthn.WebService](a.Modules().Services())
	require.NoError(t, err)
	token := func(id guid.ID, tenant string) string {
		v, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), id, tenant, "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
		require.NoError(t, err)
		return v
	}
	ownerToken, externalToken, foreignToken := token(301, "201"), token(302, "201"), token(303, "202")
	router := moduleRouter(t, a)
	call := func(method, path, token, key, version string, body any) *httptest.ResponseRecorder {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if version != "" {
			req.Header.Set("If-Match", version)
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	definition := execution.TenantVerifierDefinition{Key: "unit-tests", Name: "Current IAM tests", Layer: "AUTOMATED", State: "ACTIVE", Required: true, AppliesTo: execution.VerifierPolicy{WorkItemTypes: []string{"BUG"}, RiskLevels: []string{"HIGH"}}, Spec: execution.VerifierSpec{Argv: []string{"test-command", "PRIVATE-VERIFIER-ARGUMENT"}, ReportFormat: "NONE", StdoutTailBytes: 8192}, TimeoutMS: 1000}
	created := call("POST", "/tenants/201/verifiers", ownerToken, "registry-create", "", definition)
	require.Equal(t, 201, created.Code, created.Body.String())
	var envelope struct {
		Data execution.VerifierView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &envelope))
	id := envelope.Data.ID
	require.Positive(t, id)
	get := "/verifiers/" + id.String()
	replay := call("POST", "/tenants/201/verifiers", ownerToken, "registry-create", "", definition)
	require.Equal(t, 201, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	for _, path := range []string{"/tenants/201/verifiers", get, get + "/versions"} {
		for _, token := range []string{externalToken, foreignToken} {
			denied := call("GET", path, token, "", "", nil)
			expectedStatus := 403
			// Owned resources conceal their existence from a different tenant.
			if token == foreignToken && path != "/tenants/201/verifiers" {
				expectedStatus = 404
			}
			require.Equal(t, expectedStatus, denied.Code, path+": "+denied.Body.String())
			require.NotContains(t, denied.Body.String(), "PRIVATE-VERIFIER-ARGUMENT")
		}
	}
	require.Equal(t, 403, call("POST", "/tenants/201/verifiers", externalToken, "external-write", "", definition).Code)
	updated := call("PATCH", get, ownerToken, "registry-update", `"1"`, map[string]any{"timeout_ms": 2000, "change_note": "Bounded timeout update"})
	require.Equal(t, 200, updated.Code, updated.Body.String())
	history := call("GET", get+"/versions", ownerToken, "", "", nil)
	require.Equal(t, 200, history.Code, history.Body.String())
	require.Contains(t, history.Body.String(), `"version_no":2`)
	var audit, outbox int64
	require.NoError(t, db.Table("iam_audit_events").Where("tenant_id=201 AND event_type=? AND target_id=?", "execution.verifier_changed", id).Count(&audit).Error)
	require.EqualValues(t, 2, audit)
	require.NoError(t, db.Table("domain_events").Where("tenant_id=201 AND event_type=? AND subject_id=?", "execution.verifier_changed", id).Count(&outbox).Error)
	require.EqualValues(t, 2, outbox)
	require.NoError(t, db.Table("iam_tenant_members").Where("tenant_id=201 AND principal_id=301").Update("status", "disabled").Error)
	require.Equal(t, 403, call("POST", "/tenants/201/verifiers", ownerToken, "registry-create", "", definition).Code)
	require.Equal(t, 404, call("PATCH", get, ownerToken, "registry-update", `"1"`, map[string]any{"timeout_ms": 2000, "change_note": "Bounded timeout update"}).Code)
}
