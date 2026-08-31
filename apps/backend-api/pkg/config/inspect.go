package config

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

const RedactedValue = "[REDACTED]"

// Inspect returns the effective configuration as mapstructure-shaped data.
// Fields marked secret:"true" retain their shape while non-empty values are
// replaced. Hidden and deprecated compatibility fields are omitted; callers see
// the canonical replacement's effective value.
func Inspect(value any) (map[string]any, error) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return nil, fmt.Errorf("config: inspect requires a non-nil struct")
	}
	for reflected.IsValid() && (reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface) {
		if reflected.IsNil() {
			return nil, fmt.Errorf("config: inspect requires a non-nil struct")
		}
		reflected = reflected.Elem()
	}
	if !reflected.IsValid() || reflected.Kind() != reflect.Struct {
		return nil, fmt.Errorf("config: inspect requires a struct")
	}
	return inspectStruct(reflected)
}

func inspectStruct(value reflect.Value) (map[string]any, error) {
	typ := value.Type()
	result := make(map[string]any, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name := strings.Split(field.Tag.Get("mapstructure"), ",")[0]
		if !field.IsExported() || name == "" || name == "-" ||
			omitFromPublishedConfig(field) {
			continue
		}

		var (
			inspected any
			err       error
		)
		if strings.EqualFold(field.Tag.Get("secret"), "true") {
			inspected, err = inspectSecret(value.Field(index))
		} else {
			inspected, err = inspectValue(value.Field(index))
		}
		if err != nil {
			return nil, fmt.Errorf("config: inspect %s: %w", name, err)
		}
		result[name] = inspected
	}
	return result, nil
}

func inspectValue(value reflect.Value) (any, error) {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nil, nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil, nil
	}
	if value.Type() == durationType {
		return time.Duration(value.Int()).String(), nil
	}

	switch value.Kind() {
	case reflect.Struct:
		return inspectStruct(value)
	case reflect.Map:
		if value.IsNil() {
			return nil, nil
		}
		result := make(map[string]any, value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			inspected, err := inspectValue(iterator.Value())
			if err != nil {
				return nil, err
			}
			result[fmt.Sprint(iterator.Key().Interface())] = inspected
		}
		return result, nil
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			return nil, nil
		}
		result := make([]any, value.Len())
		for index := 0; index < value.Len(); index++ {
			inspected, err := inspectValue(value.Index(index))
			if err != nil {
				return nil, err
			}
			result[index] = inspected
		}
		return result, nil
	case reflect.Bool:
		return value.Bool(), nil
	case reflect.String:
		return value.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint(), nil
	case reflect.Float32, reflect.Float64:
		return value.Float(), nil
	default:
		return nil, fmt.Errorf("unsupported field type %s", value.Kind())
	}
}

func inspectSecret(value reflect.Value) (any, error) {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nil, nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil, nil
	}

	switch value.Kind() {
	case reflect.String:
		if value.String() == "" {
			return "", nil
		}
		return RedactedValue, nil
	case reflect.Map:
		if value.IsNil() {
			return nil, nil
		}
		result := make(map[string]any, value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			inspected, err := inspectSecret(iterator.Value())
			if err != nil {
				return nil, err
			}
			result[fmt.Sprint(iterator.Key().Interface())] = inspected
		}
		return result, nil
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			return nil, nil
		}
		result := make([]any, value.Len())
		for index := 0; index < value.Len(); index++ {
			inspected, err := inspectSecret(value.Index(index))
			if err != nil {
				return nil, err
			}
			result[index] = inspected
		}
		return result, nil
	default:
		if value.IsZero() {
			return inspectValue(value)
		}
		return RedactedValue, nil
	}
}
