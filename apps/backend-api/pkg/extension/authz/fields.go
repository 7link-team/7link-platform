package authz

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"7link.com/platform/pkg/guid"
)

// Field declares an enforced resource attribute, not a client-defined column.
type Field struct {
	Resource     string `json:"resource"`
	Name         string `json:"name"`
	Mutability   string `json:"mutability" enum:"readOnly,readWrite,immutable,writeOnly"`
	Returned     string `json:"returned" enum:"always,never,default,request"`
	Maskable     bool   `json:"maskable"`
	Configurable bool   `json:"configurable"`
}

// FieldAccess keeps read and write role rules independent. Deny always wins.
type FieldAccess struct {
	Default    string    `json:"default" enum:"allow,deny"`
	AllowRoles []guid.ID `json:"allow_roles" maxItems:"64"`
	DenyRoles  []guid.ID `json:"deny_roles" maxItems:"64"`
}

// FieldPolicy is a stored restriction of one declared field: who may read and
// write it, and how a read it denies is rendered (omitted or redacted). It can
// only narrow what the Field declaration allows.
type FieldPolicy struct {
	Read  FieldAccess `json:"read"`
	Write FieldAccess `json:"write"`
	Mask  string      `json:"mask" enum:"omit,redact"`
}

// Canonical validates and copies the declaration before persistence or use.
func (p FieldPolicy) Canonical(f Field) (FieldPolicy, error) {
	if !f.Configurable || (p.Mask != "omit" && p.Mask != "redact") || (p.Mask == "redact" && !f.Maskable) {
		return FieldPolicy{}, fmt.Errorf("invalid field policy for %s.%s", f.Resource, f.Name)
	}
	for _, access := range []*FieldAccess{&p.Read, &p.Write} {
		if access.Default != "allow" && access.Default != "deny" {
			return FieldPolicy{}, fmt.Errorf("field access default must be allow or deny")
		}
		for _, roles := range []*[]guid.ID{&access.AllowRoles, &access.DenyRoles} {
			if len(*roles) > 64 {
				return FieldPolicy{}, fmt.Errorf("too many field policy roles")
			}
			*roles = append([]guid.ID{}, (*roles)...)
			for _, id := range *roles {
				if id <= 0 {
					return FieldPolicy{}, fmt.Errorf("field policy role must be positive")
				}
			}
			slices.Sort(*roles)
			*roles = slices.Compact(*roles)
		}
	}
	if (f.Mutability == "readOnly" && (p.Write.Default == "allow" || len(p.Write.AllowRoles) > 0)) ||
		(f.Returned == "never" && (p.Read.Default == "allow" || len(p.Read.AllowRoles) > 0 || p.Mask != "omit")) {
		return FieldPolicy{}, fmt.Errorf("field policy exceeds declared attribute capabilities")
	}
	return p, nil
}

func (a FieldAccess) allows(roles []guid.ID) bool {
	for _, role := range roles {
		if slices.Contains(a.DenyRoles, role) {
			return false
		}
	}
	if a.Default == "allow" {
		return true
	}
	for _, role := range roles {
		if slices.Contains(a.AllowRoles, role) {
			return true
		}
	}
	return false
}

// FieldDecision is the effective access to one field for one caller: how it
// is read (allow, omit, redact) and whether it may be set on create or update.
type FieldDecision struct {
	Read   string `json:"read" enum:"allow,omit,redact"`
	Create bool   `json:"create"`
	Update bool   `json:"update"`
}

// DecideField applies a restriction to the code-defined attribute capabilities.
func DecideField(f Field, policy *FieldPolicy, roles []guid.ID) FieldDecision {
	read, write := true, true
	mask := "omit"
	if policy != nil {
		read, write = policy.Read.allows(roles), policy.Write.allows(roles)
		mask = policy.Mask
	}
	decision := FieldDecision{Read: "omit"}
	if f.Returned != "never" && f.Mutability != "writeOnly" {
		if read || f.Returned == "always" {
			decision.Read = "allow"
		} else if mask == "redact" && f.Maskable {
			decision.Read = "redact"
		}
	}
	decision.Create = write && f.Mutability != "readOnly"
	decision.Update = write && (f.Mutability == "readWrite" || f.Mutability == "writeOnly")
	return decision
}

// FieldConstraint is evaluated in addition to endpoint and row authorization.
// A zero value never authorizes any attribute.
type FieldConstraint struct {
	Revision int64                    `json:"revision"`
	Fields   map[string]FieldDecision `json:"fields"`
}

// FieldPermissionChecker supplies the field decisions for one caller:
// FieldConstraint(ctx, tenantID, entityID, resource, principalID).
type FieldPermissionChecker interface {
	FieldConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (FieldConstraint, error)
}

func (r *Registry) RegisterField(f Field) error {
	if !codePattern.MatchString(f.Resource) || !codePattern.MatchString(f.Name) ||
		!slices.Contains([]string{"readOnly", "readWrite", "immutable", "writeOnly"}, f.Mutability) ||
		!slices.Contains([]string{"always", "never", "default", "request"}, f.Returned) ||
		(f.Returned == "always" && f.Configurable) ||
		(f.Mutability == "writeOnly" && f.Returned != "never") {
		return fmt.Errorf("invalid field declaration %s.%s", f.Resource, f.Name)
	}
	if r.fields == nil {
		r.fields = map[string]Field{}
	}
	key := f.Resource + "." + f.Name
	if _, exists := r.fields[key]; exists {
		return fmt.Errorf("duplicate field %s", key)
	}
	r.fields[key] = f
	return nil
}

func (r *Registry) FindField(resource, name string) (Field, bool) {
	f, ok := r.fields[resource+"."+name]
	return f, ok
}

func (r *Registry) Fields(resource string) []Field {
	fields := []Field{}
	for _, f := range r.fields {
		if resource == "" || f.Resource == resource {
			fields = append(fields, f)
		}
	}
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].Resource+"."+fields[i].Name < fields[j].Resource+"."+fields[j].Name
	})
	return fields
}

// DefaultFields lists the field declarations of the IAM core catalog. Business
// modules contribute their own through Registry.Contribute.
func DefaultFields() []Field {
	return ProfileFields()
}
