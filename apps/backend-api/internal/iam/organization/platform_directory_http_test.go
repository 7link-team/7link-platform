package organization

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/internal/iam/core"
	organizationi18n "7link.com/platform/internal/iam/organization/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	gormx "7link.com/platform/pkg/extension/gorm"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const platformDirectoryOwner guid.ID = 77

// platformDirectoryChecker records which operating platform and permission the
// route asked about, so a test can prove the guard names the path owner.
type platformDirectoryChecker struct {
	organizationPermissionChecker
	allow  bool
	owners []guid.ID
	codes  []string
}

func (c *platformDirectoryChecker) CheckOperatingPlatform(_ context.Context, owner guid.ID, code string, _ guid.ID) (bool, error) {
	c.owners = append(c.owners, owner)
	c.codes = append(c.codes, code)
	return c.allow, nil
}

type platformDirectoryHTTPHarness struct {
	*organizationHarness
	groups  *GroupService
	checker *platformDirectoryChecker
	client  humatest.TestAPI
}

// newPlatformDirectoryHTTPHarness admits callers through core's real
// membership checker, which is where the owner kind of the path is enforced: a
// tenant root selected on an operating platform route must not be admitted even
// when the caller is an active member of that tenant.
func newPlatformDirectoryHTTPHarness(t *testing.T) platformDirectoryHTTPHarness {
	t.Helper()
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	require.NoError(t, organizationi18n.Register())
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants(id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES(?,'platform','operator','Operator','active',1,?,?)", platformDirectoryOwner, organizationTestNow, organizationTestNow).Error)
	require.NoError(t, h.db.Exec("CREATE TABLE sl_iam_principals (id BIGINT PRIMARY KEY, status TEXT NOT NULL)").Error)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_principals(id,status) VALUES(1,'active')").Error)
	h.seedMember(t, 1, 1, "Tenant member")
	h.seedMember(t, platformDirectoryOwner, 1, "Platform operator")

	members := core.NewMembershipChecker(h.runtime)
	require.NoError(t, members.BindForeignTables(CoreDirectoryTableNames(h.prefix), core.PrincipalTableNames{Principals: gormx.NewTableName(h.prefix, "iam_principals")}))
	groups := newBoundGroupService(t, h.runtime, h.appender, &organizationMemberChecker{prefix: h.prefix}, &organizationAdministratorGuard{}, newOrganizationTestIDs(400))
	checker := &platformDirectoryChecker{allow: true}
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), organizationHTTPTokenVerifier{}, checker, members)
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("platform-directory-http", "1.0.0"))
	registerPlatformDirectoryHTTP(api, h.service, groups, registrar)
	return platformDirectoryHTTPHarness{organizationHarness: h, groups: groups, checker: checker, client: humatest.Wrap(t, api)}
}

func (h platformDirectoryHTTPHarness) count(t *testing.T, table string, owner guid.ID) int64 {
	t.Helper()
	var count int64
	require.NoError(t, h.db.Table(h.table(table)).Where("tenant_id = ?", owner).Count(&count).Error)
	return count
}

type platformDirectoryPage[T any] struct {
	Data []T `json:"data"`
	Meta struct {
		Page respx.Page `json:"page"`
	} `json:"meta"`
}

func decodePlatformDirectory[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body T
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	return body
}

func TestPlatformDirectoryHTTPManagesDepartmentsInsideTheOperatingPlatform(t *testing.T) {
	h := newPlatformDirectoryHTTPHarness(t)
	auth := "Authorization: Bearer test"
	base := "/operating-platforms/77/departments"
	tenantDepartment, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Tenant engineering"})
	require.NoError(t, err)

	type single struct {
		Data Department `json:"data"`
	}
	parent := decodePlatformDirectory[single](t, h.client.Post(base, auth, map[string]any{"name": "Operations", "sort_order": 5})).Data
	assert.Equal(t, platformDirectoryOwner, parent.TenantID, "the department belongs to the platform in the path")
	assert.Zero(t, parent.EntityID, "platform departments carry no tenant entity")
	assert.Equal(t, "Operations", parent.Name)
	assert.Equal(t, StatusActive, parent.Status)
	child := decodePlatformDirectory[single](t, h.client.Post(base, auth, map[string]any{"name": "Support", "parent_id": parent.ID.String(), "status": "disabled"})).Data
	assert.Equal(t, parent.ID, child.ParentID)
	assert.Equal(t, "disabled", child.Status)

	page := decodePlatformDirectory[platformDirectoryPage[Department]](t, h.client.Get(base+"?limit=1", auth))
	require.Len(t, page.Data, 1)
	assert.Equal(t, parent.ID, page.Data[0].ID)
	assert.Equal(t, int64(2), page.Meta.Page.Total, "the total counts only this platform's departments")
	assert.Equal(t, 1, page.Meta.Page.Limit)
	page = decodePlatformDirectory[platformDirectoryPage[Department]](t, h.client.Get(base+"?offset=1&limit=5", auth))
	require.Len(t, page.Data, 1)
	assert.Equal(t, child.ID, page.Data[0].ID)
	for _, item := range page.Data {
		assert.NotEqual(t, tenantDepartment.ID, item.ID)
	}

	before := h.count(t, "iam_departments", platformDirectoryOwner)
	conflict := h.client.Post(base, auth, map[string]any{"name": "Operations"})
	assert.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	foreignParent := h.client.Post(base, auth, map[string]any{"name": "Borrowed", "parent_id": tenantDepartment.ID.String()})
	assert.Equal(t, http.StatusNotFound, foreignParent.Code, "a tenant department cannot parent a platform department: %s", foreignParent.Body.String())
	invalidParent := h.client.Post(base, auth, map[string]any{"name": "Invalid", "parent_id": "not-a-snowflake"})
	assert.Equal(t, http.StatusUnprocessableEntity, invalidParent.Code, invalidParent.Body.String())
	assert.Equal(t, before, h.count(t, "iam_departments", platformDirectoryOwner), "rejected writes must not insert rows")

	assert.Equal(t, []guid.ID{77, 77, 77, 77}, h.checker.owners[:4])
	assert.Equal(t, []string{"dept_create", "dept_create", "dept_view", "dept_view"}, h.checker.codes[:4])
}

func TestPlatformDirectoryHTTPManagesGroupsInsideTheOperatingPlatform(t *testing.T) {
	h := newPlatformDirectoryHTTPHarness(t)
	auth := "Authorization: Bearer test"
	base := "/operating-platforms/77/principal-groups"
	tenantGroup, err := h.groups.Create(t.Context(), 1, CreateGroup{Name: "Tenant operators"})
	require.NoError(t, err)

	type single struct {
		Data Group `json:"data"`
	}
	created := decodePlatformDirectory[single](t, h.client.Post(base, auth, map[string]any{"name": "Operators", "description": "Platform operators"})).Data
	assert.Equal(t, platformDirectoryOwner, created.TenantID)
	assert.Equal(t, "Operators", created.Name)
	assert.Equal(t, "Platform operators", created.Description)
	assert.Equal(t, GroupTypeStatic, created.Type)

	page := decodePlatformDirectory[platformDirectoryPage[Group]](t, h.client.Get(base, auth))
	require.Len(t, page.Data, 1, "tenant groups must not leak into the platform listing")
	assert.Equal(t, created.ID, page.Data[0].ID)
	assert.NotEqual(t, tenantGroup.ID, page.Data[0].ID)
	assert.Equal(t, int64(1), page.Meta.Page.Total)

	before := h.count(t, "iam_groups", platformDirectoryOwner)
	duplicate := h.client.Post(base, auth, map[string]any{"name": "Operators"})
	assert.Equal(t, http.StatusConflict, duplicate.Code, duplicate.Body.String())
	ruleOnStatic := h.client.Post(base, auth, map[string]any{"name": "Ruled", "membership_rule": map[string]any{"field": "status", "op": "eq", "value": "active"}})
	assert.Equal(t, http.StatusUnprocessableEntity, ruleOnStatic.Code, ruleOnStatic.Body.String())
	assert.Equal(t, before, h.count(t, "iam_groups", platformDirectoryOwner))
	assert.Contains(t, h.checker.codes, "group_create")
	assert.Contains(t, h.checker.codes, "group_view")
}

func TestPlatformDirectoryHTTPRejectsTenantOwnersInvalidSelectorsAndDeniedCallers(t *testing.T) {
	h := newPlatformDirectoryHTTPHarness(t)
	auth := "Authorization: Bearer test"
	departments, groups := h.count(t, "iam_departments", 1), h.count(t, "iam_groups", 1)

	// Principal 1 is an active member of tenant 1, so only the owner kind can
	// refuse these requests.
	for _, response := range []*httptest.ResponseRecorder{
		h.client.Get("/operating-platforms/1/departments", auth),
		h.client.Post("/operating-platforms/1/departments", auth, map[string]any{"name": "Smuggled"}),
		h.client.Get("/operating-platforms/1/principal-groups", auth),
		h.client.Post("/operating-platforms/1/principal-groups", auth, map[string]any{"name": "Smuggled"}),
		h.client.Get("/operating-platforms/404/departments", auth),
	} {
		assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
	assert.Equal(t, departments, h.count(t, "iam_departments", 1), "a tenant root must not receive platform writes")
	assert.Equal(t, groups, h.count(t, "iam_groups", 1))
	assert.Empty(t, h.checker.codes, "the kind check refuses before any permission is evaluated")

	// A malformed owner selector fails closed in the scope gateway, as tenant
	// selectors do, before any membership or permission lookup.
	for _, path := range []string{"/operating-platforms/abc/departments", "/operating-platforms/0/principal-groups", "/operating-platforms/077/departments"} {
		assert.Equal(t, http.StatusForbidden, h.client.Get(path, auth).Code, path)
	}
	assert.Empty(t, h.checker.codes)
	assert.Equal(t, http.StatusUnprocessableEntity, h.client.Get("/operating-platforms/77/departments?limit=0", auth).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, h.client.Post("/operating-platforms/77/principal-groups", auth, map[string]any{"name": ""}).Code)

	h.checker.allow, h.checker.codes = false, nil
	for _, response := range []*httptest.ResponseRecorder{
		h.client.Get("/operating-platforms/77/departments", auth),
		h.client.Post("/operating-platforms/77/departments", auth, map[string]any{"name": "Denied"}),
		h.client.Get("/operating-platforms/77/principal-groups", auth),
		h.client.Post("/operating-platforms/77/principal-groups", auth, map[string]any{"name": "Denied"}),
	} {
		assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
	assert.Equal(t, []string{"dept_view", "dept_create", "group_view", "group_create"}, h.checker.codes)
	assert.Zero(t, h.count(t, "iam_departments", platformDirectoryOwner), "a denied create must not insert")
	assert.Zero(t, h.count(t, "iam_groups", platformDirectoryOwner))

	h.checker.allow = true
	require.NoError(t, h.db.Exec("UPDATE sl_iam_principals SET status = 'disabled' WHERE id = 1").Error)
	assert.Equal(t, http.StatusForbidden, h.client.Get("/operating-platforms/77/departments", auth).Code, "platform membership requires an active principal")
}

func TestPlatformDirectoryHTTPReportsStorageFailures(t *testing.T) {
	h := newPlatformDirectoryHTTPHarness(t)
	auth := "Authorization: Bearer test"
	names := PublishedDirectoryTableNames(h.prefix)
	for _, table := range []gormx.TableName{names.DepartmentClosures, names.Departments, names.Groups} {
		require.NoError(t, h.db.Exec("DROP TABLE "+table.String()).Error)
	}
	for _, response := range []*httptest.ResponseRecorder{
		h.client.Get("/operating-platforms/77/departments", auth),
		h.client.Post("/operating-platforms/77/departments", auth, map[string]any{"name": "Lost"}),
		h.client.Get("/operating-platforms/77/principal-groups", auth),
		h.client.Post("/operating-platforms/77/principal-groups", auth, map[string]any{"name": "Lost"}),
	} {
		assert.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "no such table", "storage details must not leak to the caller")
	}
}
