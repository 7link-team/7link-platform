package organization

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	"github.com/stretchr/testify/assert"
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

func newSystemDepartmentHTTPClient(t *testing.T) (*organizationHarness, humatest.TestAPI) {
	t.Helper()
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	require.NoError(t, organizationi18n.Register())
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants(id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES(99,'system',?,'System','active',1,?,?)", core.SystemRootSlug, organizationTestNow, organizationTestNow).Error)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants(id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES(77,'platform','operator','Operator','active',1,?,?)", organizationTestNow, organizationTestNow).Error)
	h.service.systemPermissions = func(context.Context, *gorm.DB, string, guid.ID) (bool, error) { return true, nil }
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), organizationHTTPTokenVerifier{}, &operatingPlatformHTTPChecker{allow: true}, organizationHTTPMembershipChecker{})
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("system-directory-test", "1"))
	registerSystemDepartmentHTTP(api, h.service, registrar)
	return h, humatest.Wrap(t, api)
}

type systemDepartmentPage struct {
	Data []Department `json:"data"`
	Meta struct {
		Page respx.Page `json:"page"`
	} `json:"meta"`
}

func TestSystemDepartmentHTTPListsReadsAndRenamesSystemDepartments(t *testing.T) {
	h, client := newSystemDepartmentHTTPClient(t)
	auth := "Authorization: Bearer test"
	var single struct {
		Data Department `json:"data"`
	}
	first, err := h.service.Create(t.Context(), 99, CreateDepartment{Name: "Audit", SortOrder: 1})
	require.NoError(t, err)
	_, err = h.service.Create(t.Context(), 99, CreateDepartment{Name: "Security", SortOrder: 2})
	require.NoError(t, err)
	platform, err := h.service.Create(t.Context(), 77, CreateDepartment{Name: "Operator desk"})
	require.NoError(t, err)

	response := client.Get("/system/departments?limit=1", auth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page systemDepartmentPage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Data, 1)
	assert.Equal(t, first.ID, page.Data[0].ID)
	assert.Equal(t, int64(2), page.Meta.Page.Total, "the system listing counts only the system root's departments")

	response = client.Get("/system/departments/"+first.ID.String(), auth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &single))
	assert.Equal(t, first.ID, single.Data.ID)
	assert.Equal(t, guid.ID(99), single.Data.TenantID)
	assert.Equal(t, http.StatusNotFound, client.Get("/system/departments/"+platform.ID.String(), auth).Code, "a platform department is not a system department")

	response = client.Patch("/system/departments/"+first.ID.String(), auth, map[string]any{"name": "Compliance", "version": 1})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &single))
	assert.Equal(t, "Compliance", single.Data.Name)
	assert.Equal(t, int64(2), single.Data.Version)
	stored, err := h.service.Get(t.Context(), 99, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "Compliance", stored.Name)

	response = client.Get("/system/operating-platforms/77/departments", auth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	page = systemDepartmentPage{}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Data, 1)
	assert.Equal(t, platform.ID, page.Data[0].ID)
	assert.Equal(t, int64(1), page.Meta.Page.Total)
	assert.Equal(t, http.StatusNotFound, client.Get("/system/tenants/77/departments", auth).Code, "a platform owner is not a tenant")
	assert.Equal(t, http.StatusNotFound, client.Get("/system/operating-platforms/99/departments", auth).Code, "the system root is not an operating platform")
	assert.Equal(t, http.StatusNotFound, client.Get("/system/operating-platforms/123/departments", auth).Code)
}

func TestSystemDepartmentHTTPRejectsInvalidReferencesWithoutWriting(t *testing.T) {
	h, client := newSystemDepartmentHTTPClient(t)
	auth := "Authorization: Bearer test"
	existing, err := h.service.Create(t.Context(), 99, CreateDepartment{Name: "Audit"})
	require.NoError(t, err)
	path := "/system/departments/" + existing.ID.String()

	// 9223372036854775808 matches the digit pattern but overflows a snowflake.
	for _, owner := range []string{"abc", "0", "9223372036854775808"} {
		assert.Equal(t, http.StatusUnprocessableEntity, client.Get("/system/tenants/"+owner+"/departments", auth).Code, owner)
		assert.Equal(t, http.StatusUnprocessableEntity, client.Get("/system/operating-platforms/"+owner+"/departments", auth).Code, owner)
	}
	for _, response := range []*httptest.ResponseRecorder{
		client.Get("/system/departments/bad", auth),
		client.Patch("/system/departments/bad", auth, map[string]any{"name": "Name", "version": 1}),
		client.Delete("/system/departments/bad?version=1", auth),
		client.Post("/system/departments", auth, map[string]any{"name": "Orphan", "parent_id": "bad"}),
		client.Patch(path, auth, map[string]any{"parent_id": "bad", "version": 1}),
	} {
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	}

	h.service.systemPermissions = func(context.Context, *gorm.DB, string, guid.ID) (bool, error) {
		return false, errors.New("permission store offline")
	}
	assert.Equal(t, http.StatusInternalServerError, client.Patch(path, auth, map[string]any{"name": "Changed", "version": 1}).Code)
	h.service.systemPermissions = nil
	assert.Equal(t, http.StatusInternalServerError, client.Delete(path+"?version=1", auth).Code, "an unwired permission check fails closed")
	assert.Equal(t, http.StatusInternalServerError, client.Post("/system/departments", auth, map[string]any{"name": "Unchecked"}).Code)

	items, err := h.service.List(t.Context(), 99)
	require.NoError(t, err)
	require.Len(t, items, 1, "rejected writes must leave the system hierarchy unchanged")
	assert.Equal(t, "Audit", items[0].Name)
	assert.Equal(t, int64(1), items[0].Version)
}

func TestSystemDepartmentHTTPReportsMissingRootAndStorageFailures(t *testing.T) {
	h, client := newSystemDepartmentHTTPClient(t)
	auth := "Authorization: Bearer test"
	require.NoError(t, h.db.Exec("UPDATE sl_iam_tenants SET status = 'suspended' WHERE id = 99").Error)
	for _, response := range []*httptest.ResponseRecorder{
		client.Get("/system/departments", auth),
		client.Get("/system/departments/1", auth),
		client.Post("/system/departments", auth, map[string]any{"name": "Headless"}),
	} {
		assert.Equal(t, http.StatusInternalServerError, response.Code, "an inactive system root admits no system directory work: %s", response.Body.String())
	}
	var count int64
	require.NoError(t, h.db.Table(h.table("iam_departments")).Where("tenant_id = ?", 99).Count(&count).Error)
	assert.Zero(t, count)

	require.NoError(t, h.db.Exec("UPDATE sl_iam_tenants SET status = 'active' WHERE id = 99").Error)
	names := PublishedDirectoryTableNames(h.prefix)
	require.NoError(t, h.db.Exec("DROP TABLE "+names.DepartmentClosures.String()).Error)
	require.NoError(t, h.db.Exec("DROP TABLE "+names.Departments.String()).Error)
	for _, response := range []*httptest.ResponseRecorder{
		client.Get("/system/departments", auth),
		client.Get("/system/tenants/1/departments", auth),
	} {
		assert.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "no such table", "storage details must not leak to the caller")
	}
	require.NoError(t, h.db.Exec("DROP TABLE "+names.Tenants.String()).Error)
	assert.Equal(t, http.StatusInternalServerError, client.Get("/system/tenants/1/departments", auth).Code)
}
