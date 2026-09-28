package authz

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/infra/guid"
)

const ProfileResource = "user_profile"

var (
	ErrFieldDenied       = errors.New("field access denied")
	ErrFieldsUnavailable = errors.New("field policy evaluator unavailable")
)

// ProfileFields describes shared attributes across administrative and protocol
// views. Endpoint capabilities can restrict these further, never broaden them.
func ProfileFields() []Field {
	return []Field{
		{Resource: ProfileResource, Name: "id", Mutability: "readOnly", Returned: "always"},
		{Resource: ProfileResource, Name: "login_name", Mutability: "readWrite", Returned: "default", Maskable: true, Configurable: true},
		{Resource: ProfileResource, Name: "display_name", Mutability: "readWrite", Returned: "default", Maskable: true, Configurable: true},
		{Resource: ProfileResource, Name: "email", Mutability: "readWrite", Returned: "default", Maskable: true, Configurable: true},
	}
}

func ProfileConstraint(ctx context.Context, checker FieldPermissionChecker, tenant, actor guid.ID) (FieldConstraint, error) {
	if checker == nil {
		return FieldConstraint{}, ErrFieldsUnavailable
	}
	if tenant <= 0 || actor <= 0 {
		return FieldConstraint{}, ErrFieldDenied
	}
	constraint, err := checker.FieldConstraint(ctx, tenant, 0, ProfileResource, actor)
	if err != nil {
		return FieldConstraint{}, errors.Join(ErrFieldsUnavailable, err)
	}
	if constraint.Fields["id"].Read != "allow" {
		return FieldConstraint{}, ErrFieldDenied
	}
	return constraint, nil
}

func (c FieldConstraint) RequireRead(names ...string) error {
	for _, name := range names {
		if c.Fields[name].Read != "allow" {
			return fmt.Errorf("%w: %s", ErrFieldDenied, name)
		}
	}
	return nil
}

func (c FieldConstraint) RequireWrite(create bool, names ...string) error {
	for _, name := range names {
		decision := c.Fields[name]
		allowed := decision.Update
		if create {
			allowed = decision.Create
		}
		if !allowed {
			return fmt.Errorf("%w: %s", ErrFieldDenied, name)
		}
	}
	return nil
}

func (c FieldConstraint) ReadString(name, value string) *string {
	switch c.Fields[name].Read {
	case "allow":
		return &value
	case "redact":
		masked := "[REDACTED]"
		return &masked
	default:
		return nil
	}
}

// Email verification must not become a side channel when email is hidden or
// redacted. It is a server-controlled derivative, not an independently writable field.
func (c FieldConstraint) EmailVerified(value bool) *bool {
	if c.Fields["email"].Read == "allow" {
		return &value
	}
	return nil
}
