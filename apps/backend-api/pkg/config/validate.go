package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Process applies opt-in normalization tags and then validates constraints.
func Process(value any) error {
	if err := Normalize(value); err != nil {
		return err
	}
	return Validate(value)
}

// Normalize applies configuration normalization tags in place. The value must
// be a non-nil pointer to a struct because normalization mutates decoded data.
func Normalize(value any) error {
	if value == nil {
		return fmt.Errorf("config: normalize requires a non-nil pointer to a struct")
	}

	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("config: normalize requires a non-nil pointer to a struct")
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("config: normalize requires a pointer to a struct, got %s", v.Kind())
	}
	return normalizeStruct(v, "")
}

// Validate checks fields tagged required:"true". Nested structs, slices, and
// maps are traversed so module and dynamically keyed configuration is covered.
func Validate(value any) error {
	if value == nil {
		return fmt.Errorf("config: validate requires a non-nil struct")
	}

	v := reflect.ValueOf(value)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return fmt.Errorf("config: validate requires a non-nil struct")
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("config: validate requires a struct, got %s", v.Kind())
	}
	return validateStruct(v, "")
}

func normalizeStruct(value reflect.Value, parent string) error {
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("mapstructure"), ",")[0]
		if !field.IsExported() || name == "" || name == "-" {
			continue
		}

		path := name
		if parent != "" {
			path = parent + "." + name
		}
		fieldValue := value.Field(i)
		if strings.EqualFold(field.Tag.Get("trim"), "true") {
			if err := trimValue(fieldValue, path); err != nil {
				return err
			}
		}
		if err := normalizeChildren(fieldValue, path); err != nil {
			return err
		}
	}
	return nil
}

func normalizeChildren(value reflect.Value, path string) error {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil
	}

	switch value.Kind() {
	case reflect.Struct:
		return normalizeStruct(value, path)
	case reflect.Map:
		for _, key := range value.MapKeys() {
			entry := value.MapIndex(key)
			copy := reflect.New(entry.Type()).Elem()
			copy.Set(entry)
			if err := normalizeChildren(copy, path+"."+fmt.Sprint(key.Interface())); err != nil {
				return err
			}
			value.SetMapIndex(key, copy)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := normalizeChildren(value.Index(i), fmt.Sprintf("%s.%d", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func trimValue(value reflect.Value, path string) error {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil
	}

	switch value.Kind() {
	case reflect.String:
		if !value.CanSet() {
			return fmt.Errorf("config: %s cannot be trimmed", path)
		}
		value.SetString(strings.TrimSpace(value.String()))
		return nil
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := trimValue(value.Index(i), fmt.Sprintf("%s.%d", path, i)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("config: %s uses trim on unsupported type %s", path, value.Kind())
	}
}

func validateStruct(value reflect.Value, parent string) error {
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("mapstructure"), ",")[0]
		if !field.IsExported() || name == "" || name == "-" {
			continue
		}

		path := name
		if parent != "" {
			path = parent + "." + name
		}
		fieldValue := value.Field(i)
		if strings.EqualFold(field.Tag.Get("required"), "true") && isMissing(fieldValue) {
			return fmt.Errorf("config: %s is required", path)
		}
		if err := validateChildren(fieldValue, path); err != nil {
			return err
		}
	}
	return nil
}

func validateChildren(value reflect.Value, path string) error {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil
	}

	switch value.Kind() {
	case reflect.Struct:
		return validateStruct(value, path)
	case reflect.Map:
		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool {
			return fmt.Sprint(keys[i].Interface()) < fmt.Sprint(keys[j].Interface())
		})
		for _, key := range keys {
			entryPath := path + "." + fmt.Sprint(key.Interface())
			if err := validateChildren(value.MapIndex(key), entryPath); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := validateChildren(value.Index(i), fmt.Sprintf("%s.%d", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func isMissing(value reflect.Value) bool {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return true
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return true
	}

	switch value.Kind() {
	case reflect.String:
		return value.String() == ""
	case reflect.Map, reflect.Slice:
		return value.Len() == 0
	default:
		return value.IsZero()
	}
}
