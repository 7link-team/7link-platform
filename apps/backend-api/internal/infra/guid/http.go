package guid

import (
	"context"
	"net/http"
	"strings"

	guidi18n "7link.com/platform/internal/infra/guid/i18n"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

// RequireID parses an identifier a request supplied (a path segment, a query
// value, a body field) and answers 422 respx.InvalidID when it is empty,
// malformed, out of range, zero or negative. Parse's lenient forms stay
// accepted, so surrounding space and JSON quoting do not change a valid
// identifier. Every handler parses request identifiers through it rather than
// with its own copy, so one rule decides what a valid identifier is.
func RequireID(value string) (ID, error) {
	id, err := Parse(value)
	if err != nil || id <= 0 {
		return 0, huma.Error422UnprocessableEntity(respx.InvalidID)
	}
	return id, nil
}

// OptionalID is RequireID for an identifier the request may omit: blank input
// is the zero ID, anything else must be a valid identifier.
func OptionalID(value string) (ID, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	return RequireID(value)
}

type batchInput struct {
	Count int `path:"count" minimum:"1" maximum:"10000" doc:"number of IDs to generate"`
}

func registerHTTP(api huma.API, next func() (int64, error)) {
	huma.Register(api, huma.Operation{
		OperationID: "next-guid",
		Method:      http.MethodGet,
		Path:        "/guid",
		Summary:     "Generate a globally unique ID",
		Tags:        []string{"system"},
	}, func(ctx context.Context, _ *struct{}) (*respx.Body[ID], error) {
		id, err := next()
		if err != nil {
			return nil, huma.Error503ServiceUnavailable(guidi18n.NotReady)
		}
		return respx.OK(ctx, ID(id)), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "next-guid-batch",
		Method:      http.MethodGet,
		Path:        "/guid/{count}",
		Summary:     "Generate globally unique IDs",
		Tags:        []string{"system"},
	}, func(ctx context.Context, input *batchInput) (*respx.Body[[]ID], error) {
		ids := make([]ID, 0, input.Count)
		for range input.Count {
			id, err := next()
			if err != nil {
				return nil, huma.Error503ServiceUnavailable(guidi18n.NotReady)
			}
			ids = append(ids, ID(id))
		}
		return respx.OK(ctx, ids), nil
	})
}
