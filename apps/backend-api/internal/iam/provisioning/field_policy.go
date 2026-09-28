package provisioning

import (
	"context"
	"encoding/json"
	"strings"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	scimfilter "github.com/scim2/filter-parser/v2"
)

func (s *Service) profileConstraint(ctx context.Context, tenant, actor guid.ID) (authz.FieldConstraint, error) {
	if actor <= 0 {
		return authz.FieldConstraint{}, ErrUnauthorized
	}
	return authz.ProfileConstraint(ctx, s.fields, tenant, actor)
}

func projectUser(resource UserResource, fields authz.FieldConstraint) UserResource {
	login, display, email := resource.UserName, resource.DisplayName, ""
	if len(resource.Emails) > 0 {
		email = resource.Emails[0].Value
	}
	resource.UserName, resource.DisplayName, resource.Name, resource.Emails = "", "", nil, nil
	if value := fields.ReadString("login_name", login); value != nil {
		resource.UserName = *value
	}
	if value := fields.ReadString("display_name", display); value != nil {
		resource.DisplayName = *value
		resource.Name = &UserName{Formatted: *value}
	}
	if value := fields.ReadString("email", email); value != nil && *value != "" {
		resource.Emails = []UserEmail{{Value: *value, Type: "work", Primary: true}}
	}
	return resource
}

func profileField(name string) string {
	switch strings.ToLower(name) {
	case "username":
		return "login_name"
	case "name", "name.formatted", "displayname":
		return "display_name"
	case "emails", "emails.value":
		return "email"
	default:
		return ""
	}
}

func requireUserCreateFields(input UserInput, fields authz.FieldConstraint) error {
	names := map[string]bool{"username": true}
	if input.DisplayName != "" || input.Name.Formatted != "" {
		names["displayname"] = true
	}
	if input.Emails != nil {
		names["emails"] = true
	}
	for name := range input.provided {
		names[name] = true
	}
	for name := range names {
		if field := profileField(name); field != "" {
			if err := fields.RequireWrite(true, field); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireUserPatchFields(patch PatchRequest, fields authz.FieldConstraint) error {
	for _, op := range patch.Operations {
		if strings.TrimSpace(op.Path) == "" {
			var values map[string]json.RawMessage
			if json.Unmarshal(op.Value, &values) != nil || values == nil {
				return ErrInvalidPath
			}
			for name := range values {
				if field := profileField(name); field != "" {
					if err := fields.RequireWrite(false, field); err != nil {
						return err
					}
				}
			}
			continue
		}
		path, err := scimfilter.ParsePath([]byte(op.Path))
		if err != nil || !patchSchema(path.AttributePath, UserSchema) || path.ValueExpression != nil {
			return ErrInvalidPath
		}
		if field := profileField(patchName(path)); field != "" {
			if err := fields.RequireWrite(false, field); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireUserFilterFields(raw string, fields authz.FieldConstraint) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if len(raw) > maxFilterBytes {
		return ErrInvalidFilter
	}
	expression, err := scimfilter.ParseFilter([]byte(raw))
	if err != nil {
		return ErrInvalidFilter
	}
	nodes := 0
	var visit func(scimfilter.Expression, int) error
	visit = func(expression scimfilter.Expression, depth int) error {
		nodes++
		if depth > 16 || nodes > 64 {
			return ErrInvalidFilter
		}
		switch value := expression.(type) {
		case *scimfilter.AttributeExpression:
			name := value.AttributePath.AttributeName
			if value.AttributePath.SubAttribute != nil {
				name += "." + *value.AttributePath.SubAttribute
			}
			if field := profileField(name); field != "" {
				return fields.RequireRead(field)
			}
		case *scimfilter.ValuePath:
			if field := profileField(value.AttributePath.AttributeName); field != "" {
				if err := fields.RequireRead(field); err != nil {
					return err
				}
			}
			return visit(value.ValueFilter, depth+1)
		case *scimfilter.LogicalExpression:
			if err := visit(value.Left, depth+1); err != nil {
				return err
			}
			return visit(value.Right, depth+1)
		case *scimfilter.NotExpression:
			return visit(value.Expression, depth+1)
		default:
			return ErrInvalidFilter
		}
		return nil
	}
	return visit(expression, 0)
}
