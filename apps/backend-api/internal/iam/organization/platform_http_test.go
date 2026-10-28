package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

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
)

type operatingPlatformHTTPChecker struct {
	organizationPermissionChecker
	allow bool
	codes []string
}

func (c *operatingPlatformHTTPChecker) CheckPlatform(_ context.Context, code string, _ guid.ID) (bool, error) {
	c.codes = append(c.codes, code)
	return c.allow, nil
}

func TestOperatingPlatformHTTPPreservesLegacyCRUDAndRejectsCreation(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	require.NoError(t, organizationi18n.Register())
	h := newOrganizationHarness(t, "platform_http_")
	h.seedTenant(t, 2, TenantActive)
	bootstrap, owners := newTenantBootstrapStub(), newTenantOwnerStub()
	service := NewPlatformService(h.runtime, h.appender, newOrganizationTestIDs(1500), bootstrap.capability(), owners.capability())
	bindForeignTables(t, service.repo, h.prefix)
	checker := &operatingPlatformHTTPChecker{allow: true}
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), organizationHTTPTokenVerifier{}, checker, organizationHTTPMembershipChecker{})
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("operating-platform-http", "1.0.0"))
	registerPlatformHTTP(api, service, registrar)
	createOperation := api.OpenAPI().Paths["/operating-platforms"].Post
	require.True(t, createOperation.Deprecated)
	require.Contains(t, createOperation.Responses, "409")
	for status := range createOperation.Responses {
		require.NotEqual(t, byte('2'), status[0], "retired creation has no success response")
	}
	client := humatest.Wrap(t, api)
	auth := "Authorization: Bearer test"

	// Seed a pre-upgrade scope through the existing internal provisioning workflow.
	legacy, err := service.Create(t.Context(), newTenantForTest("operator-one", "Operator One"))
	require.NoError(t, err)
	created := client.Post("/operating-platforms", auth, map[string]any{
		"slug": "operator-two", "name": "Operator Two", "owner_principal_id": organizationTestOwnerID.String(),
	})
	require.Equal(t, http.StatusConflict, created.Code, created.Body.String())
	require.Contains(t, created.Body.String(), "Creating additional operating platforms is disabled")
	var body struct {
		Data Platform `json:"data"`
	}
	body.Data = legacy
	item := body.Data
	require.False(t, item.ID.Zero())
	require.Len(t, bootstrap.inputs, 1)
	assert.Equal(t, ScopePlatform, bootstrap.inputs[0].ScopeKind)
	assert.Equal(t, item.ID, bootstrap.inputs[0].TenantID)
	var kind string
	require.NoError(t, h.db.Table(h.table("iam_tenants")).Select("scope_kind").Where("id = ?", item.ID).Scan(&kind).Error)
	assert.Equal(t, ScopePlatform, kind)

	listed := client.Get("/operating-platforms", auth)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var list struct {
		Data []Platform `json:"data"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &list))
	require.Len(t, list.Data, 1, "tenant roots must not appear as platforms")
	assert.Equal(t, item.ID, list.Data[0].ID)
	h.seedMember(t, item.ID, 1, "Platform operator")
	checker.allow = false
	mine := client.Get("/principals/1/operating-platforms", auth)
	require.Equal(t, http.StatusOK, mine.Code, mine.Body.String())
	require.NoError(t, json.Unmarshal(mine.Body.Bytes(), &list))
	require.Len(t, list.Data, 1)
	assert.Equal(t, item.ID, list.Data[0].ID)
	assert.Equal(t, http.StatusNotFound, client.Get("/principals/2/operating-platforms", auth).Code)
	checker.allow = true
	path := "/operating-platforms/" + item.ID.String()
	assert.Equal(t, http.StatusOK, client.Get(path, auth).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, client.Get("/operating-platforms/bad", auth).Code)
	assert.Equal(t, http.StatusNotFound, client.Get("/operating-platforms/2", auth).Code)
	assert.Equal(t, http.StatusNotFound, client.Patch("/operating-platforms/2", auth, map[string]any{"name": "Foreign", "version": 1}).Code)
	assert.Equal(t, http.StatusNotFound, client.Delete("/operating-platforms/2?version=1", auth).Code)

	updated := client.Patch(path, auth, map[string]any{"name": "Operator Updated", "version": item.Version})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &body))
	assert.Equal(t, "Operator Updated", body.Data.Name)
	assert.Equal(t, http.StatusConflict, client.Patch(path, auth, map[string]any{"name": "Stale", "version": item.Version}).Code)
	version := body.Data.Version

	checker.allow = false
	for _, response := range []int{
		client.Get("/operating-platforms", auth).Code,
		client.Post("/operating-platforms", auth, map[string]any{"slug": "denied", "name": "Denied", "owner_principal_id": organizationTestOwnerID.String()}).Code,
		client.Get(path, auth).Code,
		client.Patch(path, auth, map[string]any{"name": "Denied", "version": version}).Code,
		client.Delete(fmt.Sprintf("%s?version=%d", path, version), auth).Code,
	} {
		assert.Equal(t, http.StatusForbidden, response)
	}
	stored, err := service.Get(t.Context(), item.ID)
	require.NoError(t, err)
	assert.Equal(t, "Operator Updated", stored.Name)
	assert.Equal(t, version, stored.Version)
	checker.allow = true
	deleted := client.Delete(fmt.Sprintf("%s?version=%d", path, version), auth)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	stored, err = service.Get(t.Context(), item.ID)
	require.NoError(t, err)
	assert.Equal(t, TenantDeleted, stored.Status)
	for _, code := range checker.codes {
		assert.Contains(t, []string{"operating_platform_create", "operating_platform_view", "operating_platform_update", "operating_platform_delete"}, code)
	}
}

func TestOperatingPlatformHTTPRejectsInvalidInputsAndReportsStorageFailures(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	require.NoError(t, organizationi18n.Register())
	h := newOrganizationHarness(t, "platform_http_")
	h.seedTenant(t, 2, TenantActive)
	bootstrap, owners := newTenantBootstrapStub(), newTenantOwnerStub()
	service := NewPlatformService(h.runtime, h.appender, newOrganizationTestIDs(1500), bootstrap.capability(), owners.capability())
	bindForeignTables(t, service.repo, h.prefix)
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), organizationHTTPTokenVerifier{}, &operatingPlatformHTTPChecker{allow: true}, organizationHTTPMembershipChecker{})
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("operating-platform-http", "1.0.0"))
	registerPlatformHTTP(api, service, registrar)
	client := humatest.Wrap(t, api)
	auth := "Authorization: Bearer test"
	roots := func() int64 {
		var count int64
		require.NoError(t, h.db.Table(h.table("iam_tenants")).Count(&count).Error)
		return count
	}

	assert.Equal(t, http.StatusUnprocessableEntity, client.Post("/operating-platforms", auth, map[string]any{"slug": "operator", "name": "Operator", "owner_principal_id": "not-a-snowflake"}).Code)
	assert.Equal(t, http.StatusConflict, client.Post("/operating-platforms", auth, map[string]any{"slug": "tenant-2", "name": "Taken", "owner_principal_id": organizationTestOwnerID.String()}).Code, "new platform creation is disabled")
	assert.Equal(t, http.StatusUnprocessableEntity, client.Patch("/operating-platforms/bad", auth, map[string]any{"name": "Renamed", "version": 1}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, client.Delete("/operating-platforms/bad?version=1", auth).Code)
	assert.Equal(t, int64(1), roots(), "rejected creates must not add a root")
	assert.Empty(t, bootstrap.inputs, "no root was bootstrapped")

	sqlDB, err := h.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	assert.Equal(t, http.StatusConflict, client.Post("/operating-platforms", auth, map[string]any{"slug": "offline", "name": "Offline", "owner_principal_id": organizationTestOwnerID.String()}).Code, "disabled creation must not access storage")
	for _, response := range []int{
		client.Get("/principals/1/operating-platforms", auth).Code,
		client.Get("/operating-platforms", auth).Code,
		client.Get("/operating-platforms/2", auth).Code,
	} {
		assert.Equal(t, http.StatusInternalServerError, response)
	}
}
