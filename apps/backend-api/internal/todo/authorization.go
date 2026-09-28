package todo

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/todo/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gen/field"
)

// authorizationActions are the permissions the task module contributes to the
// shared authorization registry. Every task route guard names one of them.
func authorizationActions() []authz.Action {
	return []authz.Action{
		{Resource: Name, Verb: "create", Scope: authz.ActionScopeTenant, Summary: "create tasks", DataScoped: true},
		{Resource: Name, Verb: "view", Scope: authz.ActionScopeTenant, Summary: "view tasks", DataScoped: true},
		{Resource: Name, Verb: "update", Scope: authz.ActionScopeTenant, Summary: "update and restore tasks", DataScoped: true},
		{Resource: Name, Verb: "delete", Scope: authz.ActionScopeTenant, Summary: "delete tasks", DataScoped: true},
		{Resource: Name, Verb: "export", Scope: authz.ActionScopeTenant, Summary: "export tasks", DataScoped: true},
		{Resource: Name, Verb: "import", Scope: authz.ActionScopeTenant, Summary: "import tasks", DataScoped: true},
	}
}

// authorizationFields declares the task attributes field policies may govern.
func authorizationFields() []authz.Field {
	fields := []authz.Field{}
	for _, name := range []string{"id", "version", "title", "description", "status", "priority", "due_at", "completed_at", "created_at", "created_by", "updated_at", "updated_by", "deleted_at", "deleted_by"} {
		f := authz.Field{Resource: Name, Name: name, Mutability: "readOnly", Returned: "default", Configurable: true}
		if slices.Contains([]string{"title", "description", "status", "priority", "due_at"}, name) {
			f.Mutability = "readWrite"
		}
		f.Maskable = name == "title" || name == "description"
		if name == "id" || name == "version" {
			f.Configurable = false
		}
		if name == "id" {
			f.Returned = "always"
		}
		fields = append(fields, f)
	}
	return fields
}

func (s *service) authorization(ctx context.Context, verb string) (*authn.Claims, authz.DataConstraint, error) {
	if s.writer() == nil || s.checker == nil {
		return nil, authz.DataConstraint{}, ErrUnavailable
	}
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.TenantID.Zero() || claims.EntityID.Zero() || claims.PrincipalID.Zero() {
		return nil, authz.DataConstraint{}, ErrForbidden
	}
	constraint, err := s.checker.EntityConstraint(ctx, claims.TenantID, claims.EntityID, "todo_"+verb, claims.PrincipalID)
	if err != nil {
		return nil, authz.DataConstraint{}, errors.Join(ErrUnavailable, err)
	}
	if !constraint.AllowsEntityRows(claims.EntityID) {
		return nil, authz.DataConstraint{}, ErrForbidden
	}
	return claims, constraint, nil
}

func (s *service) constrain(ctx context.Context, q query.ITodoRowDo, cols *query.Todos, verb string) (query.ITodoRowDo, error) {
	claims, constraint, err := s.authorization(ctx, verb)
	if err != nil {
		return nil, err
	}
	q = q.Where(cols.TenantID.Eq(claims.TenantID), cols.EntityID.Eq(claims.EntityID))
	if constraint.AllowAll || slices.Contains(constraint.ResourceIDs, claims.EntityID.String()) {
		return q, nil
	}
	owners, err := constraintIDs(constraint.OwnerIDs)
	if err != nil {
		return nil, err
	}
	departments, err := constraintIDs(constraint.DepartmentIDs)
	if err != nil {
		return nil, err
	}
	var scopes []field.Expr
	if len(owners) > 0 {
		scopes = append(scopes, cols.CreatedBy.In(owners...))
	}
	if len(departments) > 0 {
		scopes = append(scopes, cols.DepartmentID.In(departments...))
	}
	if len(scopes) == 0 {
		return nil, ErrForbidden
	}
	// GORM hoists a one-child OR into the outer WHERE. Only construct an OR
	// when there are multiple scopes, keeping the tenant boundary conjunctive.
	predicate := scopes[0]
	if len(scopes) > 1 {
		predicate = field.Or(scopes...)
	}
	return q.Where(predicate), nil
}

func constraintIDs(raw []string) ([]driver.Valuer, error) {
	ids := make([]driver.Valuer, 0, len(raw))
	for _, text := range raw {
		id, err := guid.Parse(text)
		if err != nil || id.Zero() {
			return nil, ErrUnavailable
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Creation never accepts ownership from a request or a spreadsheet. Department
// attribution is a snapshot of the creator's active assignment, not a moving
// join that would silently move tasks when a member changes departments.
func (s *service) creationScope(ctx context.Context, verb string) (*authn.Claims, *guid.ID, error) {
	claims, constraint, err := s.authorization(ctx, verb)
	if err != nil {
		return nil, nil, err
	}
	if s.departments == nil {
		return nil, nil, ErrUnavailable
	}
	departmentID, err := s.departments.ActiveDepartment(ctx, claims.TenantID, claims.PrincipalID)
	if err != nil {
		return nil, nil, errors.Join(ErrUnavailable, err)
	}
	if !constraint.AllowAll && !slices.Contains(constraint.ResourceIDs, claims.EntityID.String()) &&
		!slices.Contains(constraint.OwnerIDs, claims.PrincipalID.String()) &&
		(departmentID.Zero() || !slices.Contains(constraint.DepartmentIDs, departmentID.String())) {
		return nil, nil, ErrForbidden
	}
	if departmentID.Zero() {
		return claims, nil, nil
	}
	return claims, &departmentID, nil
}
