package respx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/validation"
)

// errorEnvelope keeps the public {code,message,meta,data} shape and retains
// structured validation details internally until request-locale serialization.
type errorEnvelope struct {
	status  int
	summary string
	details []errorDetail
	// causes are the internal errors behind the response. They are never
	// serialized; ReportCause logs them when the response is a server fault.
	causes []error
	// A replay already contains the first response's translated message.
	replayed       bool
	validationData bool

	Code    int    `json:"code" doc:"the HTTP status code (1..999); business codes are 100000+"`
	Message string `json:"message" doc:"human-readable error summary"`
	Meta    Meta   `json:"meta" doc:"request metadata"`
	Data    any    `json:"data" doc:"null on error, except structured request validation or business failure details"`
}

type errorDetail struct {
	message  string
	location string
}

// msgSep separates the summary key from the field detail, and detailSep joins
// multiple field details, inside errorEnvelope.Message. localize splits on them
// to translate the summary and each detail token independently.
const (
	msgSep    = ": "
	detailSep = "; "
	argSep    = "\x1f"
)

func (e *errorEnvelope) Error() string  { return e.Message }
func (e *errorEnvelope) GetStatus() int { return e.status }

// Err returns a business error carried in the envelope: a business Code
// (100000+) and an i18n message key, with meta filled from ctx. Return it from a
// handler and huma renders it as the standard error envelope; LocalizeMessage
// resolves key to the request locale at serialize time (unknown keys pass
// through unchanged). The transport status is 400 so the response stays under the
// documented error schema; the specific failure is conveyed by Code.
func Err(ctx context.Context, code int, key string) error {
	return ErrStatus(ctx, http.StatusBadRequest, code, key, nil)
}

// ErrStatus is Err for a business failure that has its own transport status,
// such as a 409 the client resolves or a 422 it can tell apart from another 422
// by Code. data is the machine-readable detail the client needs to act on the
// failure, or nil; it is the only case in which an error carries data.
func ErrStatus(ctx context.Context, status, code int, key string, data any) error {
	return &errorEnvelope{
		status:  status,
		summary: key,
		Code:    code,
		Message: key,
		Meta:    metaOf(ctx, nil),
		Data:    data,
	}
}

// RestoreError reconstructs a cached error without changing its original
// metadata, message or exact numeric detail values. Invalid cache data fails
// closed rather than producing a malformed HTTP response.
func RestoreError(status int, body []byte) (huma.StatusError, error) {
	if status < http.StatusBadRequest || status > 599 {
		return nil, fmt.Errorf("respx: invalid cached error status")
	}
	var envelope errorEnvelope
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF || envelope.Code <= 0 || envelope.Message == "" {
		return nil, fmt.Errorf("respx: invalid cached error envelope")
	}
	envelope.status, envelope.replayed = status, true
	return &envelope, nil
}

// IsStatusError reports whether err already chose its HTTP response: a huma
// status error anywhere in its chain, such as an input check's 422. A handler's
// domain-error mapper returns such an error unchanged before matching domain
// errors, because wrapping it again would report the client's mistake as 500.
// The original err is what the mapper returns, so headers attached with
// huma.ErrorWithHeaders survive.
func IsStatusError(err error) bool {
	var status huma.StatusError
	return errors.As(err, &status)
}

// phraseKey normalizes huma's built-in English error summaries to i18n keys so
// framework-generated errors (e.g. request validation) localize like the rest.
// App code should pass fully qualified i18n keys directly to huma.ErrorXxx /
// respx.Err; only huma's own fixed phrases need normalizing here.
var phraseKey = map[string]string{
	"validation failed":         messageKey("validation_failed"),
	"request body is required":  messageKey("request_body_required"),
	"request body read timeout": messageKey("request_body_timeout"),
	"cannot read request body":  messageKey("request_body_unreadable"),
}

// Install replaces huma.NewError so every built-in error response (validation,
// 4xx, 5xx) is rendered as the uniform envelope. Call once at startup BEFORE any
// huma.Register: huma's defineErrors reflects NewError's return type, so calling
// Install first also makes the generated OpenAPI document the error envelope.
// Summary and structured validation details are localized during serialization;
// arbitrary internal errors are not exposed. RequestAt is emitted in UTC;
// elapsed_ms is zero because huma builds errors without the
// request context that Timing populates. Code is the HTTP status (1..999);
// domain code mapping (100000+) can be layered on here later.
func Install() {
	installValidationMessages()
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		summary := normalizeSummary(msg)
		return &errorEnvelope{
			status:  status,
			summary: summary,
			details: detailOf(errs),
			causes:  causesOf(errs),
			Code:    status,
			Message: summary,
			Meta:    Meta{RequestAt: time.Now().UTC()},
			Data:    nil,
		}
	}
}

func normalizeSummary(message string) string {
	if key, ok := phraseKey[message]; ok {
		return key
	}
	const prefix = "request body is too large limit="
	if strings.HasPrefix(message, prefix) {
		limit := strings.TrimSuffix(strings.TrimPrefix(message, prefix), " bytes")
		return validationMessage("request_body_too_large", limit)
	}
	return message
}

// detailOf keeps only structured, client-safe validation details. Arbitrary
// internal errors are intentionally omitted from public responses.
func detailOf(errs []error) []errorDetail {
	details := make([]errorDetail, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		de, ok := err.(huma.ErrorDetailer)
		if !ok {
			continue
		}
		detail := de.ErrorDetail()
		if detail != nil && detail.Message != "" {
			details = append(details, errorDetail{message: detail.Message, location: detail.Location})
		}
	}
	return details
}

// causesOf keeps the errors detailOf leaves out: the internal failures a caller
// passes to huma.ErrorXxx(key, err) so the response can be traced to its cause
// without that cause reaching the client. An error response passed on to be
// written again (huma.WriteErr(api, ctx, mapped.GetStatus(), mapped.Error(),
// mapped)) contributes the causes it carries rather than its own message key.
func causesOf(errs []error) []error {
	var causes []error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if _, ok := err.(huma.ErrorDetailer); ok {
			continue
		}
		var envelope *errorEnvelope
		if errors.As(err, &envelope) {
			causes = append(causes, envelope.causes...)
			continue
		}
		causes = append(causes, err)
	}
	return causes
}

// ReportCause logs the internal errors behind a server-fault response (5xx),
// with ctx so the record carries the request's trace. The client sees only the
// response's i18n key; this record is where the root cause survives. Error
// responses built by huma.ErrorXxx(key, err) are reported automatically when
// they are written; a handler that answers with its own error type calls this
// directly. A request whose caller has already gone away is logged at debug:
// its context.Canceled is not a fault anyone needs to be woken for.
func ReportCause(ctx context.Context, status int, causes ...error) {
	reportCause(ctx, status, "", causes)
}

func reportCause(ctx context.Context, status int, operation string, causes []error) {
	if status < http.StatusInternalServerError {
		return
	}
	cause := errors.Join(causes...)
	if cause == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	attrs := []any{"status", status, "error", cause}
	if operation != "" {
		attrs = append(attrs, "operation", operation)
	}
	if ctx.Err() != nil {
		slog.DebugContext(ctx, "request abandoned by the caller", attrs...)
		return
	}
	slog.ErrorContext(ctx, "request failed", attrs...)
}

func validationMessage(key string, args ...string) string {
	qualified := messageKey(key)
	if len(args) == 0 {
		return qualified
	}
	return qualified + argSep + strings.Join(args, argSep)
}

// Huma exposes its validation messages as variables. Replacing them with
// stable keys keeps every framework-generated field error localizable without
// parsing English text after validation has already run.
func installValidationMessages() {
	validation.MsgUnexpectedProperty = validationMessage("validation_unexpected_property")
	validation.MsgExpectedRFC3339DateTime = validationMessage("validation_expected_rfc3339_datetime")
	validation.MsgExpectedRFC1123DateTime = validationMessage("validation_expected_rfc1123_datetime")
	validation.MsgExpectedRFC3339Date = validationMessage("validation_expected_rfc3339_date")
	validation.MsgExpectedRFC3339Time = validationMessage("validation_expected_rfc3339_time")
	validation.MsgExpectedRFC5322Email = validationMessage("validation_expected_email", "%v")
	validation.MsgExpectedRFC5890Hostname = validationMessage("validation_expected_hostname")
	validation.MsgExpectedRFC2673IPv4 = validationMessage("validation_expected_ipv4")
	validation.MsgExpectedRFC2373IPv6 = validationMessage("validation_expected_ipv6")
	validation.MsgExpectedRFCIPAddr = validationMessage("validation_expected_ip_address")
	validation.MsgExpectedRFC3986URI = validationMessage("validation_expected_uri", "%v")
	validation.MsgExpectedRFC4122UUID = validationMessage("validation_expected_uuid", "%v")
	validation.MsgExpectedRFC6570URITemplate = validationMessage("validation_expected_uri_template")
	validation.MsgExpectedRFC6901JSONPointer = validationMessage("validation_expected_json_pointer")
	validation.MsgExpectedRFC6901RelativeJSONPointer = validationMessage("validation_expected_relative_json_pointer")
	validation.MsgExpectedRegexp = validationMessage("validation_expected_regex", "%v")
	validation.MsgExpectedMatchAtLeastOneSchema = validationMessage("validation_expected_schema_match")
	validation.MsgExpectedMatchExactlyOneSchema = validationMessage("validation_expected_single_schema_match")
	validation.MsgExpectedNotMatchSchema = validationMessage("validation_expected_schema_mismatch")
	validation.MsgExpectedPropertyNameInObject = validationMessage("validation_expected_property_name")
	validation.MsgExpectedBoolean = validationMessage("validation_expected_boolean")
	validation.MsgExpectedDuration = validationMessage("validation_expected_duration", "%v")
	validation.MsgExpectedNumber = validationMessage("validation_expected_number")
	validation.MsgExpectedInteger = validationMessage("validation_expected_integer")
	validation.MsgExpectedString = validationMessage("validation_expected_string")
	validation.MsgExpectedBase64String = validationMessage("validation_expected_base64")
	validation.MsgExpectedArray = validationMessage("validation_expected_array")
	validation.MsgExpectedObject = validationMessage("validation_expected_object")
	validation.MsgExpectedArrayItemsUnique = validationMessage("validation_expected_unique_items")
	validation.MsgExpectedOneOf = validationMessage("validation_expected_one_of", "%s")
	validation.MsgExpectedMinimumNumber = validationMessage("validation_expected_minimum_number", "%v")
	validation.MsgExpectedExclusiveMinimumNumber = validationMessage("validation_expected_exclusive_minimum_number", "%v")
	validation.MsgExpectedMaximumNumber = validationMessage("validation_expected_maximum_number", "%v")
	validation.MsgExpectedExclusiveMaximumNumber = validationMessage("validation_expected_exclusive_maximum_number", "%v")
	validation.MsgExpectedNumberBeMultipleOf = validationMessage("validation_expected_multiple", "%v")
	validation.MsgExpectedMinLength = validationMessage("validation_expected_min_length", "%d")
	validation.MsgExpectedMaxLength = validationMessage("validation_expected_max_length", "%d")
	validation.MsgExpectedBePattern = validationMessage("validation_expected_pattern", "%s")
	validation.MsgExpectedMatchPattern = validationMessage("validation_expected_pattern_match", "%s")
	validation.MsgExpectedMinItems = validationMessage("validation_expected_min_items", "%d")
	validation.MsgExpectedMaxItems = validationMessage("validation_expected_max_items", "%d")
	validation.MsgExpectedMinProperties = validationMessage("validation_expected_min_properties", "%d")
	validation.MsgExpectedMaxProperties = validationMessage("validation_expected_max_properties", "%d")
	validation.MsgExpectedRequiredProperty = validationMessage("validation_required_property", "%s")
	validation.MsgExpectedDependentRequiredProperty = validationMessage("validation_dependent_property", "%s", "%s")
}
