package todo

import (
	"context"
	"net/http"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type todoPolicy struct {
	fields     map[string]authz.FieldDecision
	grants     map[string]authz.DataConstraint
	department guid.ID
	err        error
}

func (p *todoPolicy) FieldConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.FieldConstraint, error) {
	fields := p.fields
	if fields == nil {
		fields = map[string]authz.FieldDecision{}
		for _, f := range authorizationFields() {
			fields[f.Name] = authz.DecideField(f, nil, nil)
		}
	}
	return authz.FieldConstraint{Fields: fields}, p.err
}

func (p *todoPolicy) EntityConstraint(_ context.Context, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
	if p.grants == nil {
		return authz.DataConstraint{AllowAll: true}, p.err
	}
	return p.grants[permission], p.err
}
func (p *todoPolicy) ActiveDepartment(context.Context, guid.ID, guid.ID) (guid.ID, error) {
	return p.department, p.err
}
func (*todoPolicy) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	panic("task routes must use data filtering")
}
func (*todoPolicy) CheckPlatform(context.Context, string, guid.ID) (bool, error) { return false, nil }
func (*todoPolicy) CheckBulk(context.Context, guid.ID, []string, guid.ID) (map[string]bool, error) {
	return nil, nil
}
func (*todoPolicy) Constraint(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{}, nil
}
func (*todoPolicy) ExplainEntity(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.Explanation, error) {
	return authz.Explanation{}, nil
}
func (*todoPolicy) ExplainResource(context.Context, guid.ID, guid.ID, string, guid.ID, string, guid.ID) (authz.Explanation, error) {
	return authz.Explanation{}, nil
}

type todoVerifier struct{}

func (todoVerifier) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{PrincipalID: 11}, nil
}
func (todoVerifier) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) { return true, nil }

func todoRegistrar(policy *todoPolicy) *authz.Registrar {
	registry := authz.DefaultRegistry()
	if err := registry.Contribute(authorizationActions(), authorizationFields()); err != nil {
		panic(err)
	}
	return authz.NewRegistrar(registry, todoVerifier{}, policy, todoVerifier{})
}

func boundaryContext(t *testing.T, tenant, entity, principal guid.ID) context.Context {
	return authn.WithClaims(t.Context(), &authn.Claims{TenantID: tenant, EntityID: entity, PrincipalID: principal})
}

func TestTaskRowPermissionsConstrainQueriesCountsWritesAndExports(t *testing.T) {
	for _, prefix := range []string{"", "sl_"} {
		t.Run(prefix, func(t *testing.T) {
			svc, db := newTestService(t, prefix)
			svc.departments = &todoPolicy{department: 101}
			mine, err := svc.Create(todoContext(t), draft("mine"))
			require.NoError(t, err)
			peer, err := svc.Create(boundaryContext(t, 1, 10, 12), draft("department peer"))
			require.NoError(t, err)
			svc.departments = &todoPolicy{department: 102}
			hidden, err := svc.Create(boundaryContext(t, 1, 10, 13), draft("other department"))
			require.NoError(t, err)
			foreign, err := svc.Create(boundaryContext(t, 2, 20, 11), draft("foreign tenant"))
			require.NoError(t, err)
			otherEntity, err := svc.Create(boundaryContext(t, 1, 20, 11), draft("other entity"))
			require.NoError(t, err)
			legacy, err := svc.Create(todoContext(t), draft("legacy"))
			require.NoError(t, err)
			require.NoError(t, db.Table(prefix+"todos").Where("id = ?", legacy.ID).Updates(map[string]any{"tenant_id": nil, "entity_id": nil, "department_id": nil}).Error)

			for _, test := range []struct {
				name  string
				scope authz.DataConstraint
				total int64
			}{
				{"self", authz.DataConstraint{OwnerIDs: []string{"11"}}, 1},
				{"department", authz.DataConstraint{DepartmentIDs: []string{"101"}}, 2},
				{"union", authz.DataConstraint{OwnerIDs: []string{"11"}, DepartmentIDs: []string{"102"}}, 2},
				{"entity", authz.DataConstraint{ResourceIDs: []string{"10"}}, 3},
				{"all", authz.DataConstraint{AllowAll: true}, 3},
			} {
				t.Run(test.name, func(t *testing.T) {
					svc.checker = &todoPolicy{grants: map[string]authz.DataConstraint{"todo_view": test.scope, "todo_export": test.scope}}
					page, listErr := svc.List(todoContext(t), ListInput{Limit: 1})
					require.NoError(t, listErr)
					assert.Equal(t, test.total, page.Total)
					assert.Len(t, page.Rows, 1)
					exported, exportErr := svc.Export(todoContext(t), ListInput{Limit: 100})
					require.NoError(t, exportErr)
					assert.Len(t, exported.Rows, int(test.total))
				})
			}
			self := authz.DataConstraint{OwnerIDs: []string{"11"}}
			svc.checker = &todoPolicy{grants: map[string]authz.DataConstraint{"todo_view": self, "todo_update": self, "todo_delete": self}}
			for _, id := range []guid.ID{peer.ID, hidden.ID, foreign.ID, otherEntity.ID, legacy.ID} {
				_, err = svc.Get(todoContext(t), id)
				assert.ErrorIs(t, err, ErrNotFound)
				_, err = svc.Update(todoContext(t), id, 1, Patch{})
				assert.ErrorIs(t, err, ErrNotFound)
				assert.ErrorIs(t, svc.Delete(todoContext(t), id, 1), ErrNotFound)
				_, err = svc.Restore(todoContext(t), id, 1)
				assert.ErrorIs(t, err, ErrNotFound)
			}
			_, err = svc.Export(todoContext(t), ListInput{})
			assert.ErrorIs(t, err, ErrForbidden, "view permission does not imply export")
			require.NoError(t, svc.Delete(todoContext(t), mine.ID, 1))
			restored, err := svc.Restore(todoContext(t), mine.ID, 2)
			require.NoError(t, err)
			assert.Nil(t, restored.DeletedAt)
			var foreignVersion int
			require.NoError(t, db.Table(prefix+"todos").Select("version").Where("id = ?", foreign.ID).Scan(&foreignVersion).Error)
			assert.Equal(t, 1, foreignVersion)
			svc.checker = &todoPolicy{grants: map[string]authz.DataConstraint{"todo_view": {AllowAll: true, OwnerIDs: []string{"11"}, DeniedIDs: []string{"10"}}}}
			_, err = svc.List(todoContext(t), ListInput{})
			assert.ErrorIs(t, err, ErrForbidden)
		})
	}
}

func TestTaskCreationAndImportUseTrustedOwnershipAndSeparatePermissions(t *testing.T) {
	svc, db := newTestService(t, "")
	self := authz.DataConstraint{OwnerIDs: []string{"11"}}
	svc.checker = &todoPolicy{grants: map[string]authz.DataConstraint{"todo_create": self}}
	svc.departments = &todoPolicy{department: 101}
	row, err := svc.Create(todoContext(t), draft("owned"))
	require.NoError(t, err)
	assert.Equal(t, guid.ID(1), *row.TenantID)
	assert.Equal(t, guid.ID(10), *row.EntityID)
	assert.Equal(t, guid.ID(101), *row.DepartmentID)
	assert.Equal(t, guid.ID(11), row.CreatedBy)
	_, err = svc.ImportAll(todoContext(t), []Draft{draft("not permitted")})
	assert.ErrorIs(t, err, ErrForbidden)
	svc.checker = &todoPolicy{grants: map[string]authz.DataConstraint{"todo_import": {DepartmentIDs: []string{"101"}}}}
	count, err := svc.ImportAll(todoContext(t), []Draft{draft("one"), draft("two")})
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	svc.departments = &todoPolicy{department: 102}
	_, err = svc.ImportAll(todoContext(t), []Draft{draft("wrong department")})
	assert.ErrorIs(t, err, ErrForbidden)
	var stored int64
	require.NoError(t, db.Table("todos").Count(&stored).Error)
	assert.Equal(t, int64(3), stored)
	_, err = svc.Create(t.Context(), draft("anonymous"))
	assert.ErrorIs(t, err, ErrForbidden)
	_, err = svc.ImportAll(t.Context(), nil)
	assert.ErrorIs(t, err, ErrForbidden)
}

func TestTaskAuthorizationFailuresNeverFallBackToUnfilteredQueries(t *testing.T) {
	svc, _ := newTestService(t, "")
	for _, policy := range []*todoPolicy{
		{err: assert.AnError},
		{grants: map[string]authz.DataConstraint{"todo_view": {OwnerIDs: []string{"bad"}}}},
		{grants: map[string]authz.DataConstraint{"todo_view": {DepartmentIDs: []string{"0"}}}},
	} {
		svc.checker = policy
		_, err := svc.List(todoContext(t), ListInput{})
		assert.ErrorIs(t, err, ErrUnavailable)
	}
	svc.checker = &todoPolicy{}
	svc.departments = nil
	_, err := svc.Create(todoContext(t), draft("missing department reader"))
	assert.ErrorIs(t, err, ErrUnavailable)
	svc.departments = &todoPolicy{err: assert.AnError}
	_, err = svc.Create(todoContext(t), draft("failed department lookup"))
	assert.ErrorIs(t, err, ErrUnavailable)
	svc.checker = nil
	_, err = svc.List(todoContext(t), ListInput{})
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestTaskHTTPRequiresSelectorsAndDeclaredDataPermissions(t *testing.T) {
	svc, _ := newTestService(t, "")
	policy := &todoPolicy{}
	_, api := humatest.New(t)
	registerTransferHTTP(api, svc, nil, todoRegistrar(policy))
	registerHTTP(api, svc, nil, todoRegistrar(policy))
	assert.Equal(t, http.StatusNotFound, api.Get("/todos").Code)
	assert.Equal(t, http.StatusOK, api.Get("/entities/10/todos").Code)
	policy.grants = map[string]authz.DataConstraint{}
	assert.Equal(t, http.StatusForbidden, api.Get("/entities/10/todos").Code)
	for _, item := range api.OpenAPI().Paths {
		for _, operation := range []*huma.Operation{item.Get, item.Post, item.Patch, item.Put, item.Delete} {
			if operation == nil {
				continue
			}
			assert.Equal(t, true, operation.Extensions[authz.DataFilterExtensionKey])
			assert.NotEmpty(t, operation.Security)
		}
	}
}
