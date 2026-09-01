package todo

import (
	"context"
	"net/http"
	"net/url"
)

// rawQueryKey carries the request's query string to the handler.
//
// huma binds only the fields an input struct declares, and the filter
// parameters are declared in the query contract rather than as struct fields —
// that is what lets one declaration drive parsing, sorting, selection and
// self-description together. The middleware therefore stores the raw values,
// which the handler reads back.
type rawQueryKey struct{}

// RawQueryMiddleware makes the request's query string available to handlers
// that parse filters from the contract.
func RawQueryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), rawQueryKey{}, r.URL.Query())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func rawQueryFrom(ctx context.Context) url.Values {
	if values, ok := ctx.Value(rawQueryKey{}).(url.Values); ok {
		return values
	}
	return url.Values{}
}
