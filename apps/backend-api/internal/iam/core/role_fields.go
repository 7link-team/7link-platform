package core

import (
	"fmt"
	"reflect"

	"7link.com/platform/pkg/extension/huma/fieldrules"
	"github.com/danielgtaylor/huma/v2"
)

// roleFields owns both raw HTTP character bounds and the existing normalized
// service byte bounds. Direct service calls deliberately execute only the latter.
type roleFields struct {
	Name        string `json:"name" minLength:"1" maxLength:"128" normalize:"trim-space" minBytes:"1" maxBytes:"128"`
	Description string `json:"description,omitempty" maxLength:"4096" maxBytes:"4096"`
}

var roleStringRules = func() *fieldrules.StringRules {
	rules, err := fieldrules.CompileStrings(reflect.TypeFor[roleFields]())
	if err != nil {
		panic(err)
	}
	return rules
}()

func (roleFields) TransformSchema(registry huma.Registry, schema *huma.Schema) *huma.Schema {
	return roleStringRules.Project(registry, schema)
}

type rolePatchFields struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

func (rolePatchFields) TransformSchema(registry huma.Registry, schema *huma.Schema) *huma.Schema {
	return roleStringRules.Project(registry, schema)
}

type platformRoleFields struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty" maxLength:"1024"`
}

func (platformRoleFields) TransformSchema(registry huma.Registry, schema *huma.Schema) *huma.Schema {
	return roleStringRules.Project(registry, schema)
}

func normalizeRoleFields(name, description string, normalize bool) (roleFields, error) {
	fields := roleFields{Name: name, Description: description}
	var err error
	if normalize {
		err = roleStringRules.Normalize(&fields)
	} else {
		err = roleStringRules.Check(fields)
	}
	if err != nil {
		return roleFields{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	return fields, nil
}
