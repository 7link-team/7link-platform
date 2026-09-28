// Package requestids is the handler-input parse gate's fixture. Go tooling
// ignores testdata, so this file is parsed but never built.
package requestids

import "7link.com/platform/internal/infra/guid"

type request struct {
	ID   string
	Body struct{ OwnerID string }
}

func handler(in *request, input *request, raw string) {
	_, _ = guid.Parse(in.ID)
	_, _ = guid.Parse(input.Body.OwnerID)
	_, _ = guid.Parse(raw)
	_, _ = guid.RequireID(in.ID)
}
