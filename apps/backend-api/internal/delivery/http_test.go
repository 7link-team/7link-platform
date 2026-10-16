package delivery

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
)

func TestErrorMappersKeepChosenStatus(t *testing.T) {
	chosen := huma.Error422UnprocessableEntity("invalid input")
	for _, err := range []error{chosen, fmt.Errorf("input: %w", chosen), huma.ErrorWithHeaders(chosen, http.Header{"Retry-After": {"1"}})} {
		require.Same(t, err, projectHTTPError(t.Context(), err))
	}
}
