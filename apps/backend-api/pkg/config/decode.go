package config

import (
	"reflect"
	"strings"
)

// fieldTagDecodeHook applies per-field transformations to raw configuration
// data before mapstructure converts it to the destination type. For strings the
// order is trim (when enabled), default, trim default, then required validation
// after decoding.
func fieldTagDecodeHook(_ reflect.Type, target reflect.Type, data any) (any, error) {
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if target.Kind() != reflect.Struct {
		return data, nil
	}

	values, ok := data.(map[string]any)
	if !ok {
		return data, nil
	}
	for i := 0; i < target.NumField(); i++ {
		field := target.Field(i)
		name := strings.Split(field.Tag.Get("mapstructure"), ",")[0]
		if name == "" || name == "-" {
			continue
		}

		value, exists := values[name]
		trim := strings.EqualFold(field.Tag.Get("trim"), "true")
		if exists && trim {
			value = trimRawValue(value)
			values[name] = value
		}

		defaultValue, hasDefault := field.Tag.Lookup("default")
		if hasDefault && (!exists || isEmptyRawValue(value)) {
			if trim {
				defaultValue = strings.TrimSpace(defaultValue)
			}
			values[name] = defaultValue
		}
	}
	return values, nil
}

func trimRawValue(value any) any {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []string:
		for i := range typed {
			typed[i] = strings.TrimSpace(typed[i])
		}
		return typed
	case []any:
		for i := range typed {
			typed[i] = trimRawValue(typed[i])
		}
		return typed
	default:
		return value
	}
}

func isEmptyRawValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && text == ""
}
