package respx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
)

// localizer is implemented by every envelope whose Message is an i18n key. Both
// the success Envelope (value receiver) and errorEnvelope (pointer receiver)
// implement it, so LocalizeMessage can localize either without reflection.
type localizer interface {
	// localize resolves Message against the request locale carried by ctx and
	// returns the value to serialize.
	localize(ctx context.Context) any
}

// localize resolves the success Message key to the request locale. It operates on
// a copy (value receiver), leaving the caller's envelope untouched.
func (e Envelope[T]) localize(ctx context.Context) any {
	e.Message = i18n.TContext(ctx, e.Message)
	return e
}

// localize translates the summary and client-safe validation details, including
// their field locations, into the request locale.
func (e *errorEnvelope) localize(ctx context.Context) any {
	if e.replayed {
		return e
	}
	summary := e.summary
	if summary == "" {
		summary = e.Message
	}
	msg := localizeToken(ctx, summary)
	if len(e.details) > 0 {
		parts := make([]string, 0, len(e.details))
		issues := make([]ValidationIssue, 0, len(e.details))
		for _, detail := range e.details {
			issues = append(issues, validationIssue(ctx, detail))
			localized := localizeToken(ctx, detail.message)
			if localized == detail.message {
				localized = i18n.TContext(ctx, messageKey("validation_invalid_value"))
			}
			if detail.location != "" {
				localized = i18n.TContext(ctx, messageKey("validation_field"), detail.location, localized)
			}
			parts = append(parts, localized)
		}
		msg += msgSep + strings.Join(parts, detailSep)
		if e.Data == nil || e.validationData {
			e.Data = ValidationErrorData{ValidationErrors: issues}
			e.validationData = true
		}
	}
	e.Message = msg
	return e
}

func normalizeDetailToken(token string) string {
	if key, ok := frameworkDetailKey[token]; ok {
		token = key
	} else if format, ok := strings.CutPrefix(token, "invalid date/time for format "); ok {
		token = validationMessage("validation_expected_datetime_format", format)
	} else if strings.HasPrefix(token, "invalid value:") {
		token = messageKey("validation_invalid_value")
	}
	return token
}

func localizeToken(ctx context.Context, token string) string {
	parts := strings.Split(normalizeDetailToken(token), argSep)
	message := i18n.TContext(ctx, parts[0])
	if len(parts) == 1 || !strings.Contains(message, "%") {
		return message
	}
	args := make([]any, len(parts)-1)
	for index := range args {
		args[index] = parts[index+1]
	}
	return fmt.Sprintf(message, args...)
}

var frameworkDetailKey = map[string]string{
	"invalid integer":        messageKey("validation_expected_integer"),
	"invalid float":          messageKey("validation_expected_number"),
	"invalid floating value": messageKey("validation_expected_number"),
	"invalid boolean":        messageKey("validation_expected_boolean"),
	"invalid url.URL value":  messageKey("validation_expected_uri"),
	"unparsable value":       messageKey("validation_invalid_value"),
	"unsupported type":       messageKey("validation_invalid_value"),
}

// localizeBody localizes v when it is an envelope, otherwise returns it
// unchanged. Split out from LocalizeMessage so it is testable with a plain
// context.Context.
func localizeBody(ctx context.Context, v any) any {
	if lz, ok := v.(localizer); ok {
		return lz.localize(ctx)
	}
	return v
}

// LocalizeMessage is a huma response Transformer that translates the envelope
// Message into the request locale (derived from ctx by the Locale middleware)
// for every response — success, business error, and built-in framework error.
// Register it via config.Transformers before huma.Register. Non-envelope
// responses pass through untouched.
//
// Every huma response passes through here with its request context, so this is
// also where a server fault's internal cause is logged (see ReportCause): one
// place instead of every handler's error mapper.
func LocalizeMessage(ctx huma.Context, _ string, v any) (any, error) {
	if fault, ok := v.(*errorEnvelope); ok && len(fault.causes) > 0 {
		operation := ""
		if op := ctx.Operation(); op != nil {
			operation = op.OperationID
		}
		reportCause(ctx.Context(), fault.status, operation, fault.causes)
	}
	localized := localizeBody(ctx.Context(), v)
	if envelope, ok := localized.(*errorEnvelope); ok && envelope.validationData {
		if data, ok := envelope.Data.(ValidationErrorData); ok {
			for index := range data.ValidationErrors {
				data.ValidationErrors[index].Path = issuePointer(ctx.Operation(), data.ValidationErrors[index])
			}
			envelope.Data = data
		}
	}
	return localized, nil
}

// MarshalEnvelope stores the localized envelope emitted by HTTP serialization.
// Response caches use it before committing the associated business writes.
func MarshalEnvelope(ctx context.Context, v any) ([]byte, error) {
	if _, ok := v.(localizer); !ok {
		return nil, fmt.Errorf("respx: expected response envelope")
	}
	return json.Marshal(localizeBody(ctx, v))
}
