// Package errorkeys is the error-key gate's fixture. Go tooling ignores
// testdata, so this file is parsed but never built. Every rule must fire on it;
// TestErrorKeyGateRejectsItsFixture asserts which lines do.
package errorkeys

import (
	"context"
	"net/http"

	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

const unauthorized = "unauthorized"

func bare(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	respx.WriteError(w, r, http.StatusNotFound,
		"not_found",
		0)
	if ctx == nil {
		return huma.Error403Forbidden("forbidden")
	}
	if w == nil {
		return huma.Error401Unauthorized(unauthorized)
	}
	if r == nil {
		return huma.NewError(http.StatusConflict, "conflict")
	}
	return respx.Err(ctx, 100001, "business_rule")
}

func unresolved() error {
	if true {
		return huma.Error404NotFound("respx.no_such_key")
	}
	return huma.Error422UnprocessableEntity(respx.Name + ".missing")
}

// The sanctioned forms stay quiet: a namespaced constant, a literal that
// resolves, a computed message the gate cannot read, and the empty probe.
func sanctioned(err error) error {
	_ = huma.NewError(0, "")
	if err == nil {
		return huma.Error401Unauthorized(respx.Unauthorized)
	}
	if err != nil {
		return huma.Error404NotFound("respx.not_found")
	}
	return huma.Error422UnprocessableEntity(err.Error())
}
