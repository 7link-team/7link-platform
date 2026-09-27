package organization

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"7link.com/platform/internal/iam/core"
	organizationi18n "7link.com/platform/internal/iam/organization/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSystemDepartmentHTTPHierarchyOwnershipAndTransactionGuard(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	require.NoError(t, organizationi18n.Register())
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants(id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES(99,'system',?,'System','active',1,?,?)", core.SystemRootSlug, organizationTestNow, organizationTestNow).Error)
	allowed := true
	checks := 0
	h.service.systemPermissions = func(ctx context.Context, tx *gorm.DB, code string, subject guid.ID) (bool, error) {
		checks++
		require.Equal(t, guid.ID(1), subject)
		require.Contains(t, []string{"system_department_create", "system_department_update", "system_department_delete"}, code)
		_, transaction := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, transaction)
		if !allowed {
			return false, nil
		}
		return true, h.service.repo.revisions.Lock(ctx, tx, 99)
	}
	checker := &operatingPlatformHTTPChecker{allow: true}
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), organizationHTTPTokenVerifier{}, checker, organizationHTTPMembershipChecker{})
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("system-directory-test", "1"))
	registerSystemDepartmentHTTP(api, h.service, registrar)
	client := humatest.Wrap(t, api)
	auth := "Authorization: Bearer test"
	response := client.Post("/system/departments", auth, map[string]any{"name": "System support"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Data Department `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	parent := body.Data
	require.Equal(t, guid.ID(99), parent.TenantID)
	require.Zero(t, parent.EntityID)
	response = client.Post("/system/departments", auth, map[string]any{"name": "Security", "parent_id": parent.ID.String()})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	child := body.Data
	require.Equal(t, http.StatusConflict, client.Patch("/system/departments/"+parent.ID.String(), auth, map[string]any{"parent_id": child.ID.String(), "version": 1}).Code)
	require.Equal(t, http.StatusConflict, client.Delete("/system/departments/"+parent.ID.String()+"?version=1", auth).Code)
	foreign, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Tenant department"})
	require.NoError(t, err)
	response = client.Get("/system/tenants/1/departments?limit=1", auth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page struct {
		Data []Department `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Data, 1)
	require.Equal(t, foreign.ID, page.Data[0].ID)
	require.Equal(t, http.StatusNotFound, client.Get("/system/operating-platforms/1/departments", auth).Code)
	require.Equal(t, http.StatusNotFound, client.Get("/system/tenants/99/departments", auth).Code)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants(id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES(88,'platform','disabled-platform','Disabled platform','suspended',1,?,?)", organizationTestNow, organizationTestNow).Error)
	response = client.Get("/system/operating-platforms/88/departments", auth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, http.StatusNotFound, client.Get("/system/departments/"+foreign.ID.String(), auth).Code)
	require.Equal(t, http.StatusNotFound, client.Post("/system/departments", auth, map[string]any{"name": "Foreign parent", "parent_id": foreign.ID.String()}).Code)
	allowed = false
	require.Equal(t, http.StatusForbidden, client.Patch("/system/departments/"+child.ID.String(), auth, map[string]any{"name": "Denied", "version": 1}).Code)
	allowed = true
	require.Equal(t, http.StatusOK, client.Delete("/system/departments/"+child.ID.String()+"?version=1", auth).Code)
	require.Equal(t, http.StatusOK, client.Delete("/system/departments/"+parent.ID.String()+"?version=1", auth).Code)
	require.GreaterOrEqual(t, checks, 7)
	require.Equal(t, guid.ID(0), h.audits[len(h.audits)-1].TenantID)
	require.Equal(t, "99", h.audits[len(h.audits)-1].Detail["scope_id"])
}
