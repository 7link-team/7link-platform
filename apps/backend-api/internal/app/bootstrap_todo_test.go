package app

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"strconv"
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/todo"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTodoDataPermissionsWithIAM(t *testing.T) {
	newIAMTenantFixture(t).runTodoDataPermissions(t)
}

func TestFieldPolicyUpgradeKeepsPublishedMilestoneDependencies(t *testing.T) {
	f := newIAMTenantFixture(t)
	_, err := f.app.migrateModules(t.Context(), MigrationRequest{Action: MigrationDownTo, Module: "iam", To: 11})
	require.NoError(t, err)
	assert.False(t, f.db.Migrator().HasTable("iam_field_policies"))
	assert.True(t, f.db.Migrator().HasTable("iam_tenants"))
	_, err = f.app.migrateModules(t.Context(), MigrationRequest{Action: MigrationAuto})
	require.NoError(t, err, "a new core migration must not invalidate organization.constraints")
	assert.True(t, f.db.Migrator().HasTable("iam_field_policies"))
	assert.Equal(t, int64(1), f.count(t, "iam_tenants", "id = ?", iamTenantB))
}

func (f *iamTenantFixture) runTodoDataPermissions(t *testing.T) {
	createRole := func(tenant, subject guid.ID, token, name, scope string) core.APIRole {
		response := f.request(t, http.MethodPost, "/iam/roles", tenant, token, map[string]any{"name": name}, nil)
		requireStatus(t, http.StatusCreated, response)
		role := decodeEnvelope[core.APIRole](t, response)
		for _, verb := range []string{"create", "view", "update", "delete", "export", "import"} {
			response = f.request(t, http.MethodPut, "/iam/roles/"+role.ID.String()+"/permissions/todo_"+verb, tenant, token, map[string]any{}, nil)
			requireStatus(t, http.StatusOK, response)
		}
		response = f.request(t, http.MethodPut, "/iam/roles/"+role.ID.String()+"/data-scope", tenant, token, map[string]any{"scope": scope}, nil)
		requireStatus(t, http.StatusOK, response)
		response = f.request(t, http.MethodPut, "/iam/roles/"+role.ID.String()+"/members/"+subject.String(), tenant, token, map[string]any{}, nil)
		requireStatus(t, http.StatusOK, response)
		return role
	}
	createRole(iamTenantA, f.adminA.ID, f.tokenA, "Task admin A", "all")
	createRole(iamTenantB, f.adminB.ID, f.tokenB, "Task admin B", "all")
	self := createRole(iamTenantB, f.requesterB.ID, f.tokenB, "Own tasks", "self")
	createEntity := func(tenant guid.ID, token, name, parent string) core.APIEntity {
		body := map[string]any{"name": name, "type": "store"}
		if parent != "" {
			body["parent_id"] = parent
		}
		response := f.request(t, http.MethodPost, "/iam/entities", tenant, token, body, nil)
		requireStatus(t, http.StatusCreated, response)
		return decodeEnvelope[core.APIEntity](t, response)
	}
	a := createEntity(iamTenantA, f.tokenA, "Task entity A", "")
	b := createEntity(iamTenantB, f.tokenB, "Task entity B", "")
	headers := map[string]string{authz.EntityHeader: b.ID.String()}
	departmentResponse := f.request(t, http.MethodPost, "/iam/departments", iamTenantB, f.tokenB, map[string]any{"name": "Task department", "status": "active"}, nil)
	requireStatus(t, http.StatusCreated, departmentResponse)
	department := decodeEnvelope[organization.Department](t, departmentResponse)
	for _, principal := range []guid.ID{f.adminB.ID, f.requesterB.ID} {
		response := f.request(t, http.MethodPatch, "/iam/members/"+principal.String(), iamTenantB, f.tokenB, map[string]any{"department_id": department.ID.String()}, nil)
		requireStatus(t, http.StatusOK, response)
	}
	createTask := func(tenant guid.ID, token, title string, selectors map[string]string) todo.View {
		response := f.request(t, http.MethodPost, "/todos", tenant, token, map[string]any{"title": title}, selectors)
		requireStatus(t, http.StatusCreated, response)
		return decodeEnvelope[todo.View](t, response)
	}
	mine := createTask(iamTenantB, f.requester, "own task", headers)
	peer := createTask(iamTenantB, f.tokenB, "department task", headers)
	foreign := createTask(iamTenantA, f.tokenA, "foreign task", map[string]string{authz.EntityHeader: a.ID.String()})
	assert.Equal(t, int64(1), f.count(t, "todos", "id = ? AND tenant_id = ? AND entity_id = ? AND created_by = ? AND department_id = ?", mine.ID, iamTenantB, b.ID, f.requesterB.ID, department.ID))
	requireStatus(t, http.StatusUnauthorized, f.request(t, http.MethodGet, "/todos", iamTenantB, "", nil, headers))
	requireStatus(t, http.StatusForbidden, f.request(t, http.MethodGet, "/todos", iamTenantB, f.requester, nil, nil))
	response := f.request(t, http.MethodGet, "/todos?limit=1", iamTenantB, f.requester, nil, headers)
	requireStatus(t, http.StatusOK, response)
	assert.Len(t, decodeEnvelope[[]todo.View](t, response), 1)
	assert.Contains(t, string(response.Body), `"total":1`)
	for _, id := range []guid.ID{peer.ID, foreign.ID} {
		requireStatus(t, http.StatusNotFound, f.request(t, http.MethodGet, "/todos/"+id.String(), iamTenantB, f.requester, nil, headers))
		writeHeaders := map[string]string{authz.EntityHeader: b.ID.String(), "If-Match": `"1"`}
		requireStatus(t, http.StatusNotFound, f.request(t, http.MethodPatch, "/todos/"+id.String(), iamTenantB, f.requester, map[string]any{"title": "forbidden"}, writeHeaders))
		requireStatus(t, http.StatusNotFound, f.request(t, http.MethodDelete, "/todos/"+id.String(), iamTenantB, f.requester, nil, writeHeaders))
	}
	requireStatus(t, http.StatusForbidden, f.request(t, http.MethodGet, "/todos", iamTenantB, f.requester, nil, map[string]string{authz.EntityHeader: a.ID.String()}))
	response = f.request(t, http.MethodGet, "/todos/export?format=csv", iamTenantB, f.requester, nil, headers)
	requireStatus(t, http.StatusOK, response)
	assert.Contains(t, string(response.Body), "own task")
	assert.NotContains(t, string(response.Body), "department task")
	assert.NotContains(t, string(response.Body), "foreign task")
	writeHeaders := map[string]string{authz.EntityHeader: b.ID.String(), "If-Match": `"1"`}
	response = f.request(t, http.MethodPatch, "/todos/"+mine.ID.String(), iamTenantB, f.requester, map[string]any{"description": "updated by owner"}, writeHeaders)
	requireStatus(t, http.StatusOK, response)
	assert.Equal(t, `"2"`, response.Header.Get("ETag"))
	writeHeaders["If-Match"] = `"2"`
	requireStatus(t, http.StatusNoContent, f.request(t, http.MethodDelete, "/todos/"+mine.ID.String(), iamTenantB, f.requester, nil, writeHeaders))
	requireStatus(t, http.StatusNotFound, f.request(t, http.MethodGet, "/todos/"+mine.ID.String(), iamTenantB, f.requester, nil, headers))
	writeHeaders["If-Match"] = `"3"`
	response = f.request(t, http.MethodPost, "/todos/"+mine.ID.String()+"/restore", iamTenantB, f.requester, nil, writeHeaders)
	requireStatus(t, http.StatusOK, response)
	assert.Equal(t, `"4"`, response.Header.Get("ETag"))
	assert.Equal(t, int64(1), f.count(t, "todos", "id = ? AND version = 4 AND description = ? AND updated_by = ? AND deleted_at IS NULL", mine.ID, "updated by owner", f.requesterB.ID))

	response = f.request(t, http.MethodPut, "/iam/roles/"+self.ID.String()+"/data-scope", iamTenantB, f.tokenB, map[string]any{"scope": "department"}, nil)
	requireStatus(t, http.StatusOK, response)
	response = f.request(t, http.MethodGet, "/todos", iamTenantB, f.requester, nil, headers)
	requireStatus(t, http.StatusOK, response)
	assert.Len(t, decodeEnvelope[[]todo.View](t, response), 2)
	childDepartmentResponse := f.request(t, http.MethodPost, "/iam/departments", iamTenantB, f.tokenB, map[string]any{"name": "Task child department", "parent_id": department.ID.String(), "status": "active"}, nil)
	requireStatus(t, http.StatusCreated, childDepartmentResponse)
	childDepartment := decodeEnvelope[organization.Department](t, childDepartmentResponse)
	response = f.request(t, http.MethodPatch, "/iam/members/"+f.adminB.ID.String(), iamTenantB, f.tokenB, map[string]any{"department_id": childDepartment.ID.String()}, nil)
	requireStatus(t, http.StatusOK, response)
	createTask(iamTenantB, f.tokenB, "child department task", headers)
	response = f.request(t, http.MethodGet, "/todos", iamTenantB, f.requester, nil, headers)
	requireStatus(t, http.StatusOK, response)
	assert.Len(t, decodeEnvelope[[]todo.View](t, response), 2)
	response = f.request(t, http.MethodPut, "/iam/roles/"+self.ID.String()+"/data-scope", iamTenantB, f.tokenB, map[string]any{"scope": "department_and_descendants"}, nil)
	requireStatus(t, http.StatusOK, response)
	response = f.request(t, http.MethodGet, "/todos", iamTenantB, f.requester, nil, headers)
	requireStatus(t, http.StatusOK, response)
	assert.Len(t, decodeEnvelope[[]todo.View](t, response), 3)

	denyPath := "/iam/entities/" + b.ID.String() + "/role-bindings/" + self.ID.String() + "/" + f.requesterB.ID.String()
	response = f.request(t, http.MethodPut, denyPath, iamTenantB, f.tokenB, map[string]any{"effect": "deny"}, nil)
	requireStatus(t, http.StatusOK, response)
	requireStatus(t, http.StatusForbidden, f.request(t, http.MethodGet, "/todos", iamTenantB, f.requester, nil, headers))
	requireStatus(t, http.StatusForbidden, f.request(t, http.MethodPost, "/todos", iamTenantB, f.requester, map[string]any{"title": "denied"}, headers))
	response = f.request(t, http.MethodDelete, denyPath, iamTenantB, f.tokenB, nil, nil)
	requireStatus(t, http.StatusOK, response)
	response = f.request(t, http.MethodDelete, "/iam/roles/"+self.ID.String()+"/permissions/todo_view", iamTenantB, f.tokenB, nil, nil)
	requireStatus(t, http.StatusOK, response)
	requireStatus(t, http.StatusForbidden, f.request(t, http.MethodGet, "/todos", iamTenantB, f.requester, nil, headers))
	assert.Equal(t, int64(1), f.count(t, "todos", "id = ? AND version = 1 AND deleted_at IS NULL", foreign.ID))
	response = f.request(t, http.MethodPut, "/iam/roles/"+self.ID.String()+"/permissions/todo_view", iamTenantB, f.tokenB, map[string]any{}, nil)
	requireStatus(t, http.StatusOK, response)
	response = f.request(t, http.MethodDelete, "/iam/roles/"+self.ID.String()+"/members/"+f.requesterB.ID.String(), iamTenantB, f.tokenB, nil, nil)
	requireStatus(t, http.StatusOK, response)
	response = f.request(t, http.MethodPut, denyPath, iamTenantB, f.tokenB, map[string]any{"effect": "allow"}, nil)
	requireStatus(t, http.StatusOK, response)
	childEntity := createEntity(iamTenantB, f.tokenB, "Task child entity", b.ID.String())
	childHeaders := map[string]string{authz.EntityHeader: childEntity.ID.String()}
	createTask(iamTenantB, f.tokenB, "child entity task", childHeaders)
	response = f.request(t, http.MethodGet, "/todos", iamTenantB, f.requester, nil, childHeaders)
	requireStatus(t, http.StatusOK, response)
	assert.Len(t, decodeEnvelope[[]todo.View](t, response), 1)
	childDeny := "/iam/entities/" + childEntity.ID.String() + "/role-bindings/" + self.ID.String() + "/" + f.requesterB.ID.String()
	response = f.request(t, http.MethodPut, childDeny, iamTenantB, f.tokenB, map[string]any{"effect": "deny"}, nil)
	requireStatus(t, http.StatusOK, response)
	requireStatus(t, http.StatusForbidden, f.request(t, http.MethodGet, "/todos", iamTenantB, f.requester, nil, childHeaders))
	upload := func() iamHTTPResponse {
		var buffer bytes.Buffer
		form := multipart.NewWriter(&buffer)
		part, err := form.CreateFormFile("file", "tasks.csv")
		require.NoError(t, err)
		_, err = part.Write([]byte("title,priority,status,created_by\nimported task one,1,open," + f.adminA.ID.String() + "\nimported task two,5,done," + f.adminA.ID.String() + "\n"))
		require.NoError(t, err)
		require.NoError(t, form.Close())
		return f.request(t, http.MethodPost, "/todos/import", iamTenantB, f.requester, &buffer,
			map[string]string{authz.EntityHeader: b.ID.String(), "Content-Type": form.FormDataContentType()})
	}
	response = upload()
	requireStatus(t, http.StatusOK, response)
	assert.Equal(t, 2, decodeEnvelope[todo.ImportReport](t, response).Imported)
	assert.Equal(t, int64(2), f.count(t, "todos", "title LIKE ? AND tenant_id = ? AND entity_id = ? AND created_by = ? AND department_id = ?", "imported task %", iamTenantB, b.ID, f.requesterB.ID, department.ID))
	response = f.request(t, http.MethodDelete, "/iam/roles/"+self.ID.String()+"/permissions/todo_import", iamTenantB, f.tokenB, nil, nil)
	requireStatus(t, http.StatusOK, response)
	requireStatus(t, http.StatusForbidden, upload())
	assert.Equal(t, int64(2), f.count(t, "todos", "title LIKE ?", "imported task %"))
	requireStatus(t, http.StatusOK, f.request(t, http.MethodDelete, childDeny, iamTenantB, f.tokenB, nil, nil))
	f.verifyTodoFieldPolicies(t, b.ID.String(), childEntity.ID.String(), self.ID.String(), mine.ID)
}

func (f *iamTenantFixture) verifyTodoFieldPolicies(t *testing.T, entity, child, role string, taskID guid.ID) {
	headers := map[string]string{authz.EntityHeader: entity}
	childHeaders := map[string]string{authz.EntityHeader: child}
	policyPath := "/iam/field-policies/todo/description"
	roleID, err := guid.Parse(role)
	require.NoError(t, err)
	policy := authz.FieldPolicy{
		Read:  authz.FieldAccess{Default: "deny", AllowRoles: []guid.ID{roleID}, DenyRoles: []guid.ID{}},
		Write: authz.FieldAccess{Default: "deny", AllowRoles: []guid.ID{roleID}, DenyRoles: []guid.ID{}},
		Mask:  "redact",
	}
	body := map[string]any{"version": 0, "policy": policy}
	requireStatus(t, http.StatusForbidden, f.request(t, http.MethodPut, policyPath, iamTenantB, f.requester, body, nil))
	response := f.request(t, http.MethodPut, policyPath, iamTenantB, f.tokenB, body, nil)
	requireStatus(t, http.StatusOK, response)
	saved := decodeEnvelope[core.ManagedFieldPolicy](t, response)
	response = f.request(t, http.MethodGet, "/todos/"+taskID.String(), iamTenantB, f.tokenB, nil, headers)
	requireStatus(t, http.StatusOK, response)
	assert.Equal(t, "[REDACTED]", *decodeEnvelope[todo.View](t, response).Description, "tenant administrators do not bypass field restrictions")
	response = f.request(t, http.MethodGet, "/todos/"+taskID.String(), iamTenantB, f.requester, nil, headers)
	requireStatus(t, http.StatusOK, response)
	assert.Equal(t, "updated by owner", *decodeEnvelope[todo.View](t, response).Description, "entity role bindings participate in field policies")
	response = f.request(t, http.MethodPost, "/todos", iamTenantB, f.requester, map[string]any{"title": "field child", "description": "inherited secret"}, childHeaders)
	requireStatus(t, http.StatusCreated, response)
	childTask := decodeEnvelope[todo.View](t, response)
	assert.Equal(t, "inherited secret", *childTask.Description, "parent entity role is inherited by the field evaluator")
	response = f.request(t, http.MethodGet, "/iam/fields/effective?resource=todo&entity_id="+child, iamTenantB, f.requester, nil, nil)
	requireStatus(t, http.StatusOK, response)
	assert.Equal(t, "allow", decodeEnvelope[authz.FieldConstraint](t, response).Fields["description"].Read)
	response = f.request(t, http.MethodGet, "/iam/field-policies?resource=todo", iamTenantA, f.tokenA, nil, nil)
	requireStatus(t, http.StatusOK, response)
	for _, item := range decodeEnvelope[[]core.ManagedFieldPolicy](t, response) {
		assert.Zero(t, item.Version, "another tenant has no policy override")
	}
	policy.Read.DenyRoles, policy.Write.DenyRoles = []guid.ID{roleID}, []guid.ID{roleID}
	response = f.request(t, http.MethodPut, policyPath, iamTenantB, f.tokenB, map[string]any{"version": saved.Version, "policy": policy}, nil)
	requireStatus(t, http.StatusOK, response)
	updated := decodeEnvelope[core.ManagedFieldPolicy](t, response)
	for _, path := range []string{"/todos/" + childTask.ID.String(), "/todos", "/todos/export?format=csv"} {
		response = f.request(t, http.MethodGet, path, iamTenantB, f.requester, nil, childHeaders)
		requireStatus(t, http.StatusOK, response)
		assert.NotContains(t, string(response.Body), "inherited secret")
		assert.Contains(t, string(response.Body), "[REDACTED]", "policy revision must invalidate previous access immediately")
	}
	response = f.request(t, http.MethodPatch, "/todos/"+childTask.ID.String(), iamTenantB, f.requester,
		map[string]any{"description": "forbidden write"}, map[string]string{authz.EntityHeader: child, "If-Match": `"1"`})
	requireStatus(t, http.StatusForbidden, response)
	assert.Equal(t, int64(1), f.count(t, "todos", "id = ? AND description = ? AND version = 1", childTask.ID, "inherited secret"))
	requireStatus(t, http.StatusUnprocessableEntity, f.request(t, http.MethodGet, "/todos?description=like:secret", iamTenantB, f.requester, nil, childHeaders))
	requireStatus(t, http.StatusForbidden, f.request(t, http.MethodGet, "/todos?q=secret", iamTenantB, f.requester, nil, childHeaders))
	requireStatus(t, http.StatusConflict, f.request(t, http.MethodPut, policyPath, iamTenantB, f.tokenB, map[string]any{"version": saved.Version, "policy": policy}, nil))
	requireStatus(t, http.StatusOK, f.request(t, http.MethodDelete, policyPath+"?version="+strconv.FormatInt(updated.Version, 10), iamTenantB, f.tokenB, nil, nil))
	response = f.request(t, http.MethodGet, "/todos/"+childTask.ID.String(), iamTenantB, f.requester, nil, childHeaders)
	requireStatus(t, http.StatusOK, response)
	assert.Equal(t, "inherited secret", *decodeEnvelope[todo.View](t, response).Description)
}
