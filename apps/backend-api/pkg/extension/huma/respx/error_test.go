package respx

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
)

func TestIsStatusErrorRecognizesAChosenResponseAnywhereInTheChain(t *testing.T) {
	chosen := huma.Error422UnprocessableEntity(InvalidID)
	assert.True(t, IsStatusError(chosen))
	assert.True(t, IsStatusError(fmt.Errorf("handler: %w", chosen)))
	assert.True(t, IsStatusError(huma.ErrorWithHeaders(chosen, http.Header{"Retry-After": {"1"}})))
	assert.True(t, IsStatusError(Err(t.Context(), 100001, InvalidArgument)))
	assert.False(t, IsStatusError(errors.New("domain failure")))
	assert.False(t, IsStatusError(nil))
}
