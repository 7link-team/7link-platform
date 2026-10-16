package respx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/danielgtaylor/huma/v2/validation"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidationIssuesHTTPAndSchema(t *testing.T) {
	initI18n(t)
	Install()
	router := chi.NewMux()
	router.Use(Locale)
	config := huma.DefaultConfig("test", "1")
	config.CreateHooks = nil
	config.Transformers = append(config.Transformers, LocalizeMessage)
	config.OnAddOperation = append(config.OnAddOperation, DocumentValidationErrors)
	api := humachi.New(router, config)
	type body struct {
		Name   string `json:"name" minLength:"3"`
		Nested struct {
			Title string `json:"title" minLength:"3"`
		} `json:"nested"`
		Items []struct {
			Name string `json:"name" minLength:"3"`
		} `json:"items"`
		Slash string `json:"a/b~c" minLength:"3"`
	}
	calls := 0
	huma.Register(api, huma.Operation{OperationID: "validate", Method: "POST", Path: "/validate", Errors: []int{422}}, func(ctx context.Context, in *struct{ Body body }) (*Body[string], error) {
		calls++
		return OK(ctx, "saved"), nil
	})
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		t.Run(locale, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), "POST", "/validate", strings.NewReader(`{"name":"x","nested":{"title":"x"},"items":[{"name":"x"}],"a/b~c":"x"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept-Language", locale)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, 422, rec.Code, rec.Body.String())
			var response Envelope[ValidationErrorData]
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			require.Len(t, response.Data.ValidationErrors, 4)
			paths := []string{}
			for _, issue := range response.Data.ValidationErrors {
				paths = append(paths, issue.Path)
				assert.Equal(t, "body", issue.In)
				assert.Equal(t, "expected_min_length", issue.Code)
				assert.Equal(t, map[string]string{"minLength": "3"}, issue.Params)
				assert.Equal(t, localizeToken(localeCtx(locale), validationMessage("validation_expected_min_length", "3")), issue.Message)
				assert.NotContains(t, issue.Message, "respx.")
			}
			assert.ElementsMatch(t, []string{"/name", "/nested/title", "/items/0/name", "/a~1b~0c"}, paths)
			assert.NotContains(t, rec.Body.String(), `"value"`)
		})
	}
	assert.Zero(t, calls, "invalid input must not execute the handler")
	valid := httptest.NewRequestWithContext(t.Context(), "POST", "/validate", strings.NewReader(`{"name":"valid","nested":{"title":"valid"},"items":[],"a/b~c":"valid"}`))
	valid.Header.Set("Content-Type", "application/json")
	validResponse := httptest.NewRecorder()
	router.ServeHTTP(validResponse, valid)
	assert.Equal(t, http.StatusOK, validResponse.Code, validResponse.Body.String())
	assert.Equal(t, 1, calls)
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/validate", strings.NewReader(`{"nested":{"title":"yes"},"items":[],"a/b~c":"yes"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var missing Envelope[ValidationErrorData]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &missing))
	require.Len(t, missing.Data.ValidationErrors, 1)
	assert.Equal(t, "/name", missing.Data.ValidationErrors[0].Path)
	assert.Equal(t, "required_property", missing.Data.ValidationErrors[0].Code)

	schema := api.OpenAPI().Paths["/validate"].Post.Responses["422"].Content["application/json"].Schema
	require.Len(t, schema.AnyOf, 2, "business error response remains a supported alternative")
	assert.NotEmpty(t, schema.AnyOf[0].Ref)
	require.NotEmpty(t, schema.AnyOf[1].Ref)
	encoded, err := json.Marshal(api.OpenAPI())
	require.NoError(t, err)
	for _, name := range []string{"ValidationErrorData", "ValidationIssue", "validation_errors", "message_key"} {
		assert.Contains(t, string(encoded), name)
	}
}

func TestValidationIssueSafeTokens(t *testing.T) {
	initI18n(t)
	Install()
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		for _, token := range []string{fmt.Sprintf(validation.MsgExpectedRFC5322Email, "secret@example.test"), "invalid value: private-password", "unknown private SQL", validationMessage("validation_unknown", "secret")} {
			issue := validationIssue(localeCtx(locale), errorDetail{message: token, location: "body.email"})
			encoded, err := json.Marshal(issue)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "secret")
			assert.NotContains(t, string(encoded), "private")
			assert.Nil(t, issue.Params)
			assert.NotContains(t, issue.Message, "validation_")
		}
	}
	for _, source := range []string{"query", "header", "cookie", "path", "body"} {
		issue := validationIssue(localeCtx("en-US"), errorDetail{message: "invalid integer", location: source + ".count"})
		assert.Equal(t, source, issue.In)
		assert.Equal(t, "expected_integer", issue.Code)
	}
	malformed := validationIssue(localeCtx("en-US"), errorDetail{message: messageKey("validation_expected_min_length")})
	assert.Equal(t, "invalid_value", malformed.Code)
}

func TestValidationIssuePointersNeverGuessAmbiguousLocations(t *testing.T) {
	registry := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	text := &huma.Schema{Type: "string"}
	schema := &huma.Schema{Type: "object", Properties: map[string]*huma.Schema{
		"a.b": text, "a": {Type: "object", Properties: map[string]*huma.Schema{"b": text}},
		"0": text, "x/y~z": text,
		"items":   {Type: "array", Items: &huma.Schema{Type: "object", Properties: map[string]*huma.Schema{"name": text}}},
		"dynamic": {Type: "object", AdditionalProperties: text},
	}}
	op := &huma.Operation{Metadata: map[string]any{validationRegistryKey: registry}, RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {Schema: schema}}}}
	for _, test := range []struct{ location, path string }{
		{"body.a.b", ""}, {"body.0", "/0"}, {"body.x/y~z", "/x~1y~0z"},
		{"body.items[0].name", "/items/0/name"}, {"body.items[01].name", ""},
		{"body.dynamic.a.b", ""}, {"body.unknown", ""}, {"body.items[x].name", ""},
	} {
		assert.Equal(t, test.path, issuePointer(op, ValidationIssue{In: "body", Location: test.location}), test.location)
	}
	assert.Empty(t, issuePointer(nil, ValidationIssue{}))
	assert.Empty(t, issuePointer(op, ValidationIssue{In: "query", Location: "query.0"}))
}

func TestValidationDetailsPreserveBusinessDataAndRelocalize(t *testing.T) {
	initI18n(t)
	Install()
	detail := errorDetail{message: validationMessage("validation_expected_min_length", "3"), location: "body.name"}
	business := &errorEnvelope{summary: InvalidArgument, Data: map[string]string{"conflict": "existing"}, details: []errorDetail{detail}}
	business.localize(localeCtx("en-US"))
	assert.Equal(t, map[string]string{"conflict": "existing"}, business.Data)
	err := huma.NewError(422, "validation failed", &huma.ErrorDetail{Message: detail.message, Location: detail.location, Value: "private-password"}).(*errorEnvelope)
	err.localize(localeCtx("en-US"))
	english := err.Data.(ValidationErrorData).ValidationErrors[0].Message
	err.localize(localeCtx("zh-CN"))
	chinese := err.Data.(ValidationErrorData).ValidationErrors[0].Message
	assert.NotEqual(t, english, chinese)
	raw, cause := MarshalEnvelope(localeCtx("zh-CN"), err)
	require.NoError(t, cause)
	restored, cause := RestoreError(http.StatusUnprocessableEntity, raw)
	require.NoError(t, cause)
	replay, cause := MarshalEnvelope(localeCtx("en-US"), restored)
	require.NoError(t, cause)
	assert.JSONEq(t, string(raw), string(replay), "replay preserves original localized error and issues")
}

func TestValidationRuleAllowlistHasThreeLanguageMessages(t *testing.T) {
	initI18n(t)
	for code, names := range validationRuleParams {
		args := make([]string, len(names))
		for index := range args {
			args[index] = "3"
		}
		for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
			issue := validationIssue(localeCtx(locale), errorDetail{message: validationMessage("validation_"+code, args...)})
			assert.Equal(t, code, issue.Code)
			assert.Equal(t, messageKey("validation_"+code), issue.MessageKey)
			assert.NotContains(t, issue.Message, "validation_", code+" "+locale)
			assert.NotContains(t, issue.Message, "%!", code+" "+locale)
			assert.Len(t, issue.Params, len(names))
		}
	}
}

func TestValidationMessagesReuseResponseCatalog(t *testing.T) {
	initI18n(t)
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		t.Run(locale, func(t *testing.T) {
			ctx := localeCtx(locale)
			rules, issues := ValidationMessages(ctx)
			require.Len(t, issues, len(validationRuleParams))
			for code, names := range validationRuleParams {
				item := issues[messageKey("validation_"+code)]
				require.Equal(t, code, item.Code)
				require.NotContains(t, item.Template, "respx.")
				require.NotContains(t, item.Template, "%")
				rendered := item.Template
				args := make([]string, len(names))
				for index, name := range names {
					args[index] = fmt.Sprintf("value%d", index)
					require.Contains(t, rendered, "{"+name+"}")
					rendered = strings.ReplaceAll(rendered, "{"+name+"}", args[index])
				}
				require.Equal(t, localizeToken(ctx, validationMessage("validation_"+code, args...)), rendered)
			}
			for id, item := range rules {
				require.Equal(t, issues[item.MessageKey].Code, item.Code, id)
				require.NotContains(t, item.Template, "%", id)
				require.NotContains(t, item.Template, "respx.", id)
			}
			require.Equal(t, localizeToken(ctx, validationMessage("validation_expected_min_bytes", "{min}")), rules["custom:minBytes"].Template)
			require.Equal(t, localizeToken(ctx, validationMessage("validation_expected_max_length", "{max}")), rules["maxLength"].Template)
			require.Equal(t, localizeToken(ctx, validationMessage("validation_expected_one_of", "{allowed}")), rules["enum"].Template)
			rules["minLength"] = ValidationMessage{Template: "mutated"}
			delete(issues, messageKey("validation_expected_min_length"))
			nextRules, nextIssues := ValidationMessages(ctx)
			require.NotEqual(t, "mutated", nextRules["minLength"].Template)
			require.Contains(t, nextIssues, messageKey("validation_expected_min_length"))
		})
	}
}
