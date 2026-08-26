package docs

import (
	_ "embed"
	"net/http"
)

// apidocs.swagger.json is the merged OpenAPI document for every proto module,
// produced by protoc-gen-openapiv2 (allow_merge) into this package's spec/ dir.
//
//go:embed spec/apidocs.swagger.json
var specJSON []byte

// SpecPath is where the raw OpenAPI document is served; the UI pages point their
// loaders at it.
const SpecPath = "/openapi.json"

// Handler serves the landing page, the six documentation UIs and the raw
// OpenAPI document. Anything it does not own is passed to next, so the caller
// can hand it the API mux as the fallback.
func Handler(name string, next http.Handler) http.Handler {
	pages := map[string][]byte{
		"/":                indexHTML(name, SpecPath),
		"/docs":            docsWrapperHTML(name, SpecPath),
		"/docs/swagger":    swaggerHTML(name, SpecPath),
		"/docs/redoc":      redocHTML(name, SpecPath),
		"/docs/rapidoc":    rapidocHTML(name, SpecPath),
		"/docs/scalar":     scalarHTML(name, SpecPath),
		"/docs/stoplight":  stoplightHTML(name, SpecPath),
		"/docs/openapi-ui": openapiUIHTML(name, SpecPath),
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == SpecPath {
			w.Header().Set("Content-Type", "application/json")
			w.Write(specJSON)
			return
		}
		if body, ok := pages[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(body)
			return
		}
		next.ServeHTTP(w, r)
	})
}
