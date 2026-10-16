package respx

import (
	"context"
	"reflect"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// ValidationIssue is a safe, localizable request failure. Rejected values are
// deliberately absent. Path is present only when the request schema identifies
// one unambiguous JSON Pointer; Location is Huma's original diagnostic location.
type ValidationIssue struct {
	In         string            `json:"in" enum:"body,query,path,header,cookie"`
	Path       string            `json:"path,omitempty"`
	Location   string            `json:"location,omitempty"`
	Code       string            `json:"code"`
	MessageKey string            `json:"message_key"`
	Params     map[string]string `json:"params,omitempty"`
	Message    string            `json:"message"`
}

type ValidationErrorData struct {
	ValidationErrors []ValidationIssue `json:"validation_errors"`
}

// Rule parameter names are an allowlist for framework-owned message tokens.
// Parser error strings (email/URI/etc.) are never published as parameters.
var validationRuleParams = map[string][]string{
	"invalid_value": nil, "unexpected_property": nil,
	"expected_rfc3339_datetime": nil, "expected_rfc1123_datetime": nil,
	"expected_rfc3339_date": nil, "expected_rfc3339_time": nil,
	"expected_datetime_format": {"format"}, "expected_email": nil,
	"expected_hostname": nil, "expected_ipv4": nil, "expected_ipv6": nil,
	"expected_ip_address": nil, "expected_uri": nil, "expected_uuid": nil,
	"expected_uri_template": nil, "expected_json_pointer": nil,
	"expected_relative_json_pointer": nil, "expected_regex": nil,
	"expected_schema_match": nil, "expected_single_schema_match": nil,
	"expected_schema_mismatch": nil, "expected_property_name": nil,
	"expected_boolean": nil, "expected_duration": nil, "expected_number": nil,
	"expected_integer": nil, "expected_string": nil, "expected_base64": nil,
	"expected_array": nil, "expected_object": nil, "expected_unique_items": nil,
	"expected_one_of": {"values"}, "expected_minimum_number": {"minimum"},
	"expected_exclusive_minimum_number": {"minimum"},
	"expected_maximum_number":           {"maximum"}, "expected_exclusive_maximum_number": {"maximum"},
	"expected_multiple": {"multipleOf"}, "expected_min_length": {"minLength"},
	"expected_max_length": {"maxLength"},
	"expected_min_bytes":  {"minBytes"}, "expected_max_bytes": {"maxBytes"}, "expected_pattern": {"pattern"},
	"expected_pattern_match": {"pattern"}, "expected_min_items": {"minItems"},
	"expected_max_items": {"maxItems"}, "expected_min_properties": {"minProperties"},
	"expected_max_properties": {"maxProperties"}, "required_property": {"property"},
	"dependent_property": {"property", "dependency"},
}

// ValidationMessage is a literal-only template derived from the same catalog
// used by validation responses. It contains no rejected values or executable code.
type ValidationMessage struct {
	Code       string `json:"code"`
	MessageKey string `json:"message_key"`
	Template   string `json:"template"`
}

// ValidationMessages publishes the framework rule vocabulary and issue messages
// for the request locale. Each call owns its maps; operation snapshots are not mutated.
func ValidationMessages(ctx context.Context) (map[string]ValidationMessage, map[string]ValidationMessage) {
	issues := make(map[string]ValidationMessage, len(validationRuleParams))
	for code, params := range validationRuleParams {
		key := messageKey("validation_" + code)
		args := make([]string, len(params))
		for index, param := range params {
			args[index] = "{" + param + "}"
		}
		issues[key] = ValidationMessage{Code: code, MessageKey: key, Template: localizeToken(ctx, validationMessage("validation_"+code, args...))}
	}
	// Only parameter names change at this boundary, never wording or rule limits.
	definitions := map[string]struct {
		code   string
		params []string
	}{
		"invalid":          {"invalid_value", nil},
		"type":             {"invalid_value", nil},
		"required":         {"required_property", []string{"property"}},
		"number":           {"expected_number", nil},
		"integer":          {"expected_integer", nil},
		"minLength":        {"expected_min_length", []string{"min"}},
		"maxLength":        {"expected_max_length", []string{"max"}},
		"minimum":          {"expected_minimum_number", []string{"minimum"}},
		"maximum":          {"expected_maximum_number", []string{"maximum"}},
		"exclusiveMinimum": {"expected_exclusive_minimum_number", []string{"minimum"}},
		"exclusiveMaximum": {"expected_exclusive_maximum_number", []string{"maximum"}},
		"minItems":         {"expected_min_items", []string{"min"}},
		"maxItems":         {"expected_max_items", []string{"max"}},
		"pattern":          {"expected_pattern", []string{"pattern"}},
		"enum":             {"expected_one_of", []string{"allowed"}},
		"format:date":      {"expected_rfc3339_date", nil},
		"format:date-time": {"expected_rfc3339_datetime", nil},
		"custom:minBytes":  {"expected_min_bytes", []string{"min"}},
		"custom:maxBytes":  {"expected_max_bytes", []string{"max"}},
	}
	rules := make(map[string]ValidationMessage, len(definitions))
	for id, definition := range definitions {
		item := issues[messageKey("validation_"+definition.code)]
		// Replace only trusted, declared placeholder names from the shared allowlist.
		for index, name := range validationRuleParams[definition.code] {
			item.Template = strings.ReplaceAll(item.Template, "{"+name+"}", "{"+definition.params[index]+"}")
		}
		rules[id] = item
	}
	return rules, issues
}

func validationIssue(ctx context.Context, detail errorDetail) ValidationIssue {
	token := normalizeDetailToken(detail.message)
	parts := strings.Split(token, argSep)
	code := strings.TrimPrefix(parts[0], messageKey("validation_"))
	names, known := validationRuleParams[code]
	if !strings.HasPrefix(parts[0], messageKey("validation_")) || !known || len(parts)-1 < len(names) {
		code, names, parts = "invalid_value", nil, []string{messageKey("validation_invalid_value")}
	}
	issue := ValidationIssue{In: "body", Location: detail.location, Code: code, MessageKey: parts[0]}
	for _, source := range []string{"body", "query", "path", "header", "cookie"} {
		if detail.location == source || strings.HasPrefix(detail.location, source+".") {
			issue.In = source
			break
		}
	}
	safe := parts[0]
	if len(names) > 0 {
		issue.Params = make(map[string]string, len(names))
		for index, name := range names {
			issue.Params[name] = parts[index+1]
		}
		safe += argSep + strings.Join(parts[1:len(names)+1], argSep)
	}
	issue.Message = localizeToken(ctx, safe)
	return issue
}

const validationRegistryKey = "respx.validation.registry"

// DocumentValidationErrors augments, rather than replaces, existing business
// 422 schemas. Called from the shared Huma configuration after typed registration.
func DocumentValidationErrors(doc *huma.OpenAPI, op *huma.Operation) {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[validationRegistryKey] = doc.Components.Schemas
	response := op.Responses["422"]
	if response == nil {
		if op.RequestBody == nil && len(op.Parameters) == 0 {
			return
		}
		response = op.Responses["default"]
		if response == nil {
			return
		}
	}
	media := response.Content["application/json"]
	if media == nil || media.Schema == nil {
		return
	}
	schema := doc.Components.Schemas.Schema(reflect.TypeFor[Envelope[ValidationErrorData]](), true, "ValidationErrorEnvelope")
	previous := media.Schema
	// Responses can be shared by operations: never mutate their shared media.
	copied := *response
	copied.Content = make(map[string]*huma.MediaType, len(response.Content))
	for key, value := range response.Content {
		copied.Content[key] = value
	}
	typed := *media
	typed.Schema = &huma.Schema{AnyOf: []*huma.Schema{previous, schema}}
	copied.Content["application/json"] = &typed
	op.Responses["422"] = &copied
}

func issuePointer(op *huma.Operation, issue ValidationIssue) string {
	if op == nil || issue.In != "body" || op.RequestBody == nil {
		return ""
	}
	registry, ok := op.Metadata[validationRegistryKey].(huma.Registry)
	if !ok {
		return ""
	}
	media := op.RequestBody.Content["application/json"]
	if media == nil {
		return ""
	}
	candidates := map[string]bool{}
	ambiguous := false
	var visit func(*huma.Schema, string, string, int)
	visit = func(schema *huma.Schema, location, pointer string, depth int) {
		if schema == nil || depth > 64 {
			return
		}
		if schema.Ref != "" {
			schema = registry.Map()[strings.TrimPrefix(schema.Ref, "#/components/schemas/")]
			if schema == nil {
				return
			}
		}
		if schema.AdditionalProperties != nil && schema.AdditionalProperties != false &&
			strings.HasPrefix(issue.Location, location+".") {
			ambiguous = true
		}
		if location == issue.Location {
			if issue.Code == "required_property" || issue.Code == "dependent_property" {
				name := issue.Params["property"]
				if _, exists := schema.Properties[name]; exists {
					escaped := strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
					candidates[pointer+"/"+escaped] = true
				}
			} else {
				candidates[pointer] = true
			}
		}
		for name, child := range schema.Properties {
			next := location + "." + name
			if issue.Location == next || strings.HasPrefix(issue.Location, next+".") || strings.HasPrefix(issue.Location, next+"[") {
				escaped := strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
				visit(child, next, pointer+"/"+escaped, depth+1)
			}
		}
		if schema.Items != nil && strings.HasPrefix(issue.Location, location+"[") {
			tail := strings.TrimPrefix(issue.Location, location+"[")
			if end := strings.IndexByte(tail, ']'); end >= 0 {
				index := tail[:end]
				if n, err := strconv.ParseUint(index, 10, 64); err == nil && strconv.FormatUint(n, 10) == index {
					visit(schema.Items, location+"["+index+"]", pointer+"/"+index, depth+1)
				}
			}
		}
		for _, union := range [][]*huma.Schema{schema.AllOf, schema.AnyOf, schema.OneOf} {
			for _, child := range union {
				visit(child, location, pointer, depth+1)
			}
		}
	}
	visit(media.Schema, "body", "", 0)
	if ambiguous || len(candidates) != 1 {
		return ""
	}
	for pointer := range candidates {
		return pointer
	}
	return ""
}

// ByteLengthDetail describes a normalized UTF-8 byte bound without retaining input.
func ByteLengthDetail(location string, minimum bool, limit int) *huma.ErrorDetail {
	key := "validation_expected_max_bytes"
	if minimum {
		key = "validation_expected_min_bytes"
	}
	return &huma.ErrorDetail{Location: location, Message: validationMessage(key, strconv.Itoa(limit))}
}
