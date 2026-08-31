package appenv

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRuntimeCloseIsReverseOrderAndIdempotent(t *testing.T) {
	var order []string
	want := errors.New("close failed")
	runtime := NewRuntime(NewIdentity("api", "1"))
	runtime.closers = append(runtime.closers,
		func() error {
			order = append(order, "first")
			return want
		},
		func() error {
			order = append(order, "second")
			return nil
		},
	)

	assert.ErrorIs(t, runtime.Close(), want)
	assert.ErrorIs(t, runtime.Close(), want)
	assert.Equal(t, []string{"second", "first"}, order)
	assert.NoError(t, (*Runtime)(nil).Close())
}
