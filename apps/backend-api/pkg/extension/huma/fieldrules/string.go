// Package fieldrules projects module-owned extended field tags into Huma schemas
// and executes the same rules at service boundaries.
package fieldrules

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// StringIssue contains no rejected input. The HTTP boundary chooses its response.
type StringIssue struct {
	Field, Rule string
	Limit       int
}

func (e *StringIssue) Error() string { return fmt.Sprintf("%s: %s %d", e.Field, e.Rule, e.Limit) }

type stringField struct {
	field            reflect.StructField
	name             string
	minimum, maximum *int
	trim             bool
}

// StringRules is immutable after compilation. Validation uses only local state.
type StringRules struct {
	model  reflect.Type
	fields []stringField
}

// CompileStrings supports the currently needed post-normalization tags only:
// normalize:"trim-space", minBytes and maxBytes. Raw Huma rules are not executed
// here: HTTP and direct service calls intentionally have different input profiles.
func CompileStrings(model reflect.Type) (*StringRules, error) {
	if model == nil || model.Kind() != reflect.Struct {
		return nil, fmt.Errorf("string rules require a struct")
	}
	rules := &StringRules{model: model}
	for index := 0; index < model.NumField(); index++ {
		field := model.Field(index)
		minimum, hasMin := field.Tag.Lookup("minBytes")
		maximum, hasMax := field.Tag.Lookup("maxBytes")
		normalize, hasNormalize := field.Tag.Lookup("normalize")
		if !hasMin && !hasMax && !hasNormalize {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.PkgPath != "" || field.Type.Kind() != reflect.String || name == "" || name == "-" {
			return nil, fmt.Errorf("string rules require a named exported string: %s", field.Name)
		}
		rule := stringField{field: field, name: name}
		if hasNormalize {
			if normalize != "trim-space" {
				return nil, fmt.Errorf("unsupported normalization on %s", field.Name)
			}
			rule.trim = true
		}
		for _, bound := range []struct {
			raw     string
			present bool
			target  **int
		}{{minimum, hasMin, &rule.minimum}, {maximum, hasMax, &rule.maximum}} {
			if !bound.present {
				continue
			}
			value, err := strconv.Atoi(bound.raw)
			if err != nil || value < 0 {
				return nil, fmt.Errorf("invalid byte bound on %s", field.Name)
			}
			*bound.target = &value
		}
		if rule.minimum != nil && rule.maximum != nil && *rule.minimum > *rule.maximum {
			return nil, fmt.Errorf("inverted byte bounds on %s", field.Name)
		}
		rules.fields = append(rules.fields, rule)
	}
	return rules, nil
}

// Normalize validates a copy and commits normalized values only on success.
func (r *StringRules) Normalize(value any) error {
	target := reflect.ValueOf(value)
	if target.Kind() != reflect.Pointer || target.IsNil() || target.Elem().Type() != r.model {
		return fmt.Errorf("string rules require a pointer to %s", r.model)
	}
	normalized := reflect.New(r.model).Elem()
	normalized.Set(target.Elem())
	for _, rule := range r.fields {
		field := normalized.FieldByIndex(rule.field.Index)
		value := field.String()
		if rule.trim {
			value = strings.TrimSpace(value)
		}

		field.SetString(value)
	}
	if err := r.Check(normalized.Interface()); err != nil {
		return err
	}
	target.Elem().Set(normalized)
	return nil
}

// Check validates an already normalized record without changing untouched fields.
func (r *StringRules) Check(value any) error {
	record := reflect.ValueOf(value)
	if !record.IsValid() || record.Type() != r.model {
		return fmt.Errorf("string rules require %s", r.model)
	}
	for _, rule := range r.fields {
		value := record.FieldByIndex(rule.field.Index).String()
		if rule.minimum != nil && len(value) < *rule.minimum {
			return &StringIssue{Field: rule.name, Rule: "minBytes", Limit: *rule.minimum}
		}
		if rule.maximum != nil && len(value) > *rule.maximum {
			return &StringIssue{Field: rule.name, Rule: "maxBytes", Limit: *rule.maximum}
		}
	}
	return nil
}

// Project runs in SchemaTransformer, before Huma precomputes validation. Existing
// operation-specific raw length tags take precedence; otherwise they come from
// the shared declaration. Pointer nullability/required semantics are untouched.
func (r *StringRules) Project(registry huma.Registry, schema *huma.Schema) *huma.Schema {
	for _, rule := range r.fields {
		property := schema.Properties[rule.name]
		if property == nil {
			continue
		}
		source := huma.SchemaFromField(registry, rule.field, r.model.Name()+rule.field.Name)
		if property.MinLength == nil {
			property.MinLength = source.MinLength
		}
		if property.MaxLength == nil {
			property.MaxLength = source.MaxLength
		}
		args := map[string]any{}
		if rule.minimum != nil {
			args["min"] = *rule.minimum
		}
		if rule.maximum != nil {
			args["max"] = *rule.maximum
		}
		if rule.trim {
			args["normalize"] = "trim-space"
		}
		if property.Extensions == nil {
			property.Extensions = map[string]any{}
		}
		property.Extensions["x-field-rules"] = []map[string]any{{"rule": "utf8-bytes", "args": args}}
	}
	return schema
}
