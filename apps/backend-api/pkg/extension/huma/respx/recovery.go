package respx

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/felixge/httpsnoop"
)

// Recovery converts panics raised by downstream HTTP handlers into the
// platform error envelope when the response has not started yet.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		wroteResponse := false
		wrapped := httpsnoop.Wrap(response, httpsnoop.Hooks{
			WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
				return func(status int) {
					wroteResponse = true
					next(status)
				}
			},
			Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
				return func(body []byte) (int, error) {
					wroteResponse = true
					return next(body)
				}
			},
		})
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.ErrorContext(request.Context(), "HTTP handler panic recovered",
					"panic", recovered,
					"stack", string(debug.Stack()),
				)
				if !wroteResponse {
					WriteError(response, request, http.StatusInternalServerError, messageKey("internal_server_error"), 0)
				}
			}
		}()
		next.ServeHTTP(wrapped, request)
	})
}
